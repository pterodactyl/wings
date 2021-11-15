package parser

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/Jeffail/gabs/v2"
	"github.com/apex/log"
	"github.com/buger/jsonparser"
	"github.com/iancoleman/strcase"
)

// Regex to match anything that has a value matching the format of {{ config.$1 }} which
// will cause the program to lookup that configuration value from itself and set that
// value to the configuration one.
//
// This allows configurations to reference values that are node dependent, such as the
// internal IP address used by the daemon, useful in Bungeecord setups for example, where
// it is common to see variables such as "{{config.docker.interface}}"
var configMatchRegex = regexp.MustCompile(`{{\s?config\.([\w.-]+)\s?}}`)

// Regex to support modifying XML inline variable data using the config tools. This means
// you can pass a replacement of Root.Property='[value="testing"]' to get an XML node
// matching:
//
// <Root>
//   <Property value="testing"/>
// </Root>
//
// noinspection RegExpRedundantEscape
var xmlValueMatchRegex = regexp.MustCompile(`^\[([\w]+)='(.*)'\]$`)

// Gets the []byte representation of a configuration file to be passed through to other
// handler functions. If the file does not currently exist, it will be created.
func readFileBytes(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

// Gets the value of a key based on the value type defined.
func (cfr *ConfigurationFileReplacement) getKeyValue(value string) interface{} {
	if cfr.ReplaceWith.Type() == jsonparser.Boolean {
		v, _ := strconv.ParseBool(value)
		return v
	}

	// Try to parse into an int, if this fails just ignore the error and continue
	// through, returning the string.
	if v, err := strconv.Atoi(value); err == nil {
		return v
	}

	return value
}

// Iterate over an unstructured JSON/YAML/etc. interface and set all of the required
// key/value pairs for the configuration file.
//
// We need to support wildcard characters in key searches, this allows you to make
// modifications to multiple keys at once, especially useful for games with multiple
// configurations per-world (such as Spigot and Bungeecord) where we'll need to make
// adjustments to the bind address for the user.
//
// This does not currently support nested wildcard matches. For example, foo.*.bar
// will work, however foo.*.bar.*.baz will not, since we'll only be splitting at the
// first wildcard, and not subsequent ones.
func (f *ConfigurationFile) IterateOverJson(data []byte) (*gabs.Container, error) {
	parsed, err := gabs.ParseJSON(data)
	if err != nil {
		return nil, err
	}

	for _, v := range f.Replace {
		value, err := f.LookupConfigurationValue(v)
		if err != nil {
			return nil, err
		}

		// Check for a wildcard character, and if found split the key on that value to
		// begin doing a search and replace in the data.
		if strings.Contains(v.Match, ".*") {
			parts := strings.SplitN(v.Match, ".*", 2)

			// Iterate over each matched child and set the remaining path to the value
			// that is passed through in the loop.
			//
			// If the child is a null value, nothing will happen. Seems reasonable as of the
			// time this code is being written.
			for _, child := range parsed.Path(strings.Trim(parts[0], ".")).Children() {
				if err := v.SetAtPathway(child, strings.Trim(parts[1], "."), value); err != nil {
					if errors.Is(err, gabs.ErrNotFound) {
						continue
					}
					return nil, errors.WithMessage(err, "failed to set config value of array child")
				}
			}
			continue
		}

		if err := v.SetAtPathway(parsed, v.Match, value); err != nil {
			if errors.Is(err, gabs.ErrNotFound) {
				continue
			}
			return nil, errors.WithMessage(err, "unable to set config value at pathway: "+v.Match)
		}
	}

	return parsed, nil
}

// Regex used to check if there is an array element present in the given pathway by looking for something
// along the lines of "something[1]" or "something[1].nestedvalue" as the path.
var checkForArrayElement = regexp.MustCompile(`^([^\[\]]+)\[([\d]+)](\..+)?$`)

// Attempt to set the value of the path depending on if it is an array or not. Gabs cannot handle array
// values as "something[1]" but can parse them just fine. This is basically just overly complex code
// to handle that edge case and ensure the value gets set correctly.
//
// Bless thee who has to touch these most unholy waters.
func setValueAtPath(c *gabs.Container, path string, value interface{}) error {
	var err error

	matches := checkForArrayElement.FindStringSubmatch(path)

	// Check if we are **NOT** updating an array element.
	if len(matches) < 3 {
		_, err = c.SetP(value, path)
		return err
	}

	i, _ := strconv.Atoi(matches[2])
	// Find the array element "i" or try to create it if "i" is equal to 0 and is not found
	// at the given path.
	ct, err := c.ArrayElementP(i, matches[1])
	if err != nil {
		if i != 0 || (!errors.Is(err, gabs.ErrNotArray) && !errors.Is(err, gabs.ErrNotFound)) {
			return errors.WithMessage(err, "error while parsing array element at path")
		}

		t := make([]interface{}, 1)
		// If the length of matches is 4 it means we're trying to access an object down in this array
		// key, so make sure we generate the array as an array of objects, and not just a generic nil
		// array.
		if len(matches) == 4 {
			t = []interface{}{map[string]interface{}{}}
		}

		// If the error is because this isn't an array or isn't found go ahead and create the array with
		// an empty object if we have additional things to set on the array, or just an empty array type
		// if there is not an object structure detected (no matches[3] available).
		if _, err = c.SetP(t, matches[1]); err != nil {
			return errors.WithMessage(err, "failed to create empty array for missing element")
		}

		// Set our cursor to be the array element we expect, which in this case is just the first element
		// since we won't run this code unless the array element is 0. There is too much complexity in trying
		// to match additional elements. In those cases the server will just have to be rebooted or something.
		ct, err = c.ArrayElementP(0, matches[1])
		if err != nil {
			return errors.WithMessage(err, "failed to find array element at path")
		}
	}

	// Try to set the value. If the path does not exist an error will be raised to the caller which will
	// then check if the error is because the path is missing. In those cases we just ignore the error since
	// we don't want to do anything specifically when that happens.
	//
	// If there are four matches in the regex it means that we managed to also match a trailing pathway
	// for the key, which should be found in the given array key item and modified further.
	if len(matches) == 4 {
		_, err = ct.SetP(value, strings.TrimPrefix(matches[3], "."))
	} else {
		_, err = ct.Set(value)
	}

	if err != nil {
		return errors.WithMessage(err, "failed to set value at config path: "+path)
	}

	return nil
}

// Sets the value at a specific pathway, but checks if we were looking for a specific
// value or not before doing it.
func (cfr *ConfigurationFileReplacement) SetAtPathway(c *gabs.Container, path string, value string) error {
	if cfr.IfValue == "" {
		return setValueAtPath(c, path, cfr.getKeyValue(value))
	}

	// Check if we are replacing instead of overwriting.
	if strings.HasPrefix(cfr.IfValue, "regex:") {
		// Doing a regex replacement requires an existing value.
		// TODO: Do we try passing an empty string to the regex?
		if c.ExistsP(path) {
			return gabs.ErrNotFound
		}

		r, err := regexp.Compile(strings.TrimPrefix(cfr.IfValue, "regex:"))
		if err != nil {
			log.WithFields(log.Fields{"if_value": strings.TrimPrefix(cfr.IfValue, "regex:"), "error": err}).
				Warn("configuration if_value using invalid regexp, cannot perform replacement")
			return nil
		}

		v := strings.Trim(c.Path(path).String(), "\"")
		if r.Match([]byte(v)) {
			return setValueAtPath(c, path, r.ReplaceAllString(v, value))
		}
		return nil
	}

	if c.ExistsP(path) && !bytes.Equal(c.Bytes(), []byte(cfr.IfValue)) {
		return nil
	}

	return setValueAtPath(c, path, cfr.getKeyValue(value))
}

// Looks up a configuration value on the Daemon given a dot-notated syntax.
func (f *ConfigurationFile) LookupConfigurationValue(cfr ConfigurationFileReplacement) (string, error) {
	// If this is not something that we can do a regex lookup on then just continue
	// on our merry way. If the value isn't a string, we're not going to be doing anything
	// with it anyways.
	if cfr.ReplaceWith.Type() != jsonparser.String || !configMatchRegex.Match(cfr.ReplaceWith.Value()) {
		return cfr.ReplaceWith.String(), nil
	}

	// If there is a match, lookup the value in the configuration for the Daemon. If no key
	// is found, just return the string representation, otherwise use the value from the
	// daemon configuration here.
	huntPath := configMatchRegex.ReplaceAllString(
		configMatchRegex.FindString(cfr.ReplaceWith.String()), "$1",
	)

	var path []string
	for _, value := range strings.Split(huntPath, ".") {
		path = append(path, strcase.ToSnake(value))
	}

	// Look for the key in the configuration file, and if found return that value to the
	// calling function.
	match, _, _, err := jsonparser.Get(f.configuration, path...)
	if err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return string(match), err
		}

		log.WithFields(log.Fields{"path": path, "filename": f.FileName}).Debug("attempted to load a configuration value that does not exist")

		// If there is no key, keep the original value intact, that way it is obvious there
		// is a replace issue at play.
		return string(match), nil
	} else {
		return configMatchRegex.ReplaceAllString(cfr.ReplaceWith.String(), string(match)), nil
	}
}
