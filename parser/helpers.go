package parser

import (
	"bytes"
	"github.com/Jeffail/gabs/v2"
	"github.com/buger/jsonparser"
	"github.com/iancoleman/strcase"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"io/ioutil"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
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
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return ioutil.ReadAll(file)
}

// Gets the value of a key based on the value type defined.
func getKeyValue(value []byte) interface{} {
	if reflect.ValueOf(value).Kind() == reflect.Bool {
		v, _ := strconv.ParseBool(string(value))
		return v
	}

	// Try to parse into an int, if this fails just ignore the error and
	if v, err := strconv.Atoi(string(value)); err == nil {
		return v
	}

	return string(value)
}

// Iterate over an unstructured JSON/YAML/etc. interface and set all of the required
// key/value pairs for the configuration file.
//
// We need to support wildcard characters in key searches, this allows you to make
// modifications to multiple keys at once, especially useful for games with multiple
// configurations per-world (such as Spigot and Bungeecord) where we'll need to make
// adjustments to the bind address for the user.
//
// This does not currently support nested matches. container.*.foo.*.bar will not work.
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
					return nil, err
				}
			}
		} else {
			if err = v.SetAtPathway(parsed, v.Match, value); err != nil {
				return nil, err
			}
		}
	}

	return parsed, nil
}

// Sets the value at a specific pathway, but checks if we were looking for a specific
// value or not before doing it.
func (cfr *ConfigurationFileReplacement) SetAtPathway(c *gabs.Container, path string, value []byte) error {
	if cfr.IfValue != "" {
		// If this is a regex based matching, we need to get a little more creative since
		// we're only going to replacing part of the string, and not the whole thing.
		if c.Exists(path) && strings.HasPrefix(cfr.IfValue, "regex:") {
			// We're doing some regex here.
			r, err := regexp.Compile(strings.TrimPrefix(cfr.IfValue, "regex:"))
			if err != nil {
				zap.S().Warnw(
					"configuration if_value using invalid regexp, cannot do replacement",
					zap.String("if_value", strings.TrimPrefix(cfr.IfValue, "regex:")),
					zap.Error(err),
				)
				return nil
			}

			// If the path exists and there is a regex match, go ahead and attempt the replacement
			// using the value we got from the key. This will only replace the one match.
			v := strings.Trim(string(c.Path(path).Bytes()), "\"")
			if r.Match([]byte(v)) {
				_, err := c.SetP(r.ReplaceAllString(v, string(value)), path)

				return err
			}

			return nil
		} else {
			if !c.Exists(path) || (c.Exists(path) && !bytes.Equal(c.Bytes(), []byte(cfr.IfValue))) {
				return nil
			}
		}
	}

	_, err := c.SetP(getKeyValue(value), path)

	return err
}

// Looks up a configuration value on the Daemon given a dot-notated syntax.
func (f *ConfigurationFile) LookupConfigurationValue(cfr ConfigurationFileReplacement) ([]byte, error) {
	// If this is not something that we can do a regex lookup on then just continue
	// on our merry way. If the value isn't a string, we're not going to be doing anything
	// with it anyways.
	if cfr.ReplaceWith.Type() != jsonparser.String || !configMatchRegex.Match(cfr.ReplaceWith.Value()) {
		return cfr.ReplaceWith.Value(), nil
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
			return match, errors.WithStack(err)
		}

		zap.S().Debugw(
			"attempted to load a configuration value that does not exist",
			zap.Strings("path", path),
			zap.String("filename", f.FileName),
		)

		// If there is no key, keep the original value intact, that way it is obvious there
		// is a replace issue at play.
		return match, nil
	} else {
		replaced := []byte(configMatchRegex.ReplaceAllString(cfr.ReplaceWith.String(), string(match)))

		return replaced, nil
	}
}
