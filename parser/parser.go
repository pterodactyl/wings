package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/Jeffail/gabs/v2"
	"github.com/buger/jsonparser"
	"github.com/ghodss/yaml"
	"github.com/iancoleman/strcase"
	"github.com/magiconair/properties"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"go.uber.org/zap"
	"io/ioutil"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The file parsing options that are available for a server configuration file.
const (
	File       = "file"
	Yaml       = "yaml"
	Properties = "properties"
	Ini        = "ini"
	Json       = "json"
	Xml        = "xml"
)

// Regex to match anything that has a value matching the format of {{ config.$1 }} which
// will cause the program to lookup that configuration value from itself and set that
// value to the configuration one.
//
// This allows configurations to reference values that are node dependent, such as the
// internal IP address used by the daemon, useful in Bungeecord setups for example, where
// it is common to see variables such as "{{config.docker.interface}}"
var configMatchRegex = regexp.MustCompile(`^{{\s?config\.([\w.-]+)\s?}}$`)

type ConfigurationParser string

// Defines a configuration file for the server startup. These will be looped over
// and modified before the server finishes booting.
type ConfigurationFile struct {
	FileName string                         `json:"file"`
	Parser   ConfigurationParser            `json:"parser"`
	Replace  []ConfigurationFileReplacement `json:"replace"`

	// Tracks Wings' configuration so that we can quickly get values
	// out of it when variables request it.
	configuration []byte
}

// Defines a single find/replace instance for a given server configuration file.
type ConfigurationFileReplacement struct {
	Match     string               `json:"match"`
	Value     string               `json:"value"`
	ValueType jsonparser.ValueType `json:"-"`
}

func (cfr *ConfigurationFileReplacement) UnmarshalJSON(data []byte) error {
	if m, err := jsonparser.GetString(data, "match"); err != nil {
		return err
	} else {
		cfr.Match = m
	}

	if v, dt, _, err := jsonparser.Get(data, "value"); err != nil {
		return err
	} else {
		if dt != jsonparser.String && dt != jsonparser.Number && dt != jsonparser.Boolean {
			return errors.New(
				fmt.Sprintf("cannot parse JSON: received unexpected replacement value type: %d", dt),
			)
		}

		cfr.Value = string(v)
		cfr.ValueType = dt
	}

	return nil
}

// Parses a given configuration file and updates all of the values within as defined
// in the API response from the Panel.
func (f *ConfigurationFile) Parse(path string) error {
	zap.S().Debugw("parsing configuration file", zap.String("path", path), zap.String("parser", string(f.Parser)))

	mb, _ := json.Marshal(config.Get())
	f.configuration = mb

	var err error

	switch f.Parser {
	case Properties:
		err = f.parsePropertiesFile(path)
		break
	case File:
		err = f.parseTextFile(path)
		break
	case Yaml, "yml":
		err = f.parseYamlFile(path)
		break
	case Json:
		err = f.parseJsonFile(path)
		break
	}

	return err
}

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

// Helper function to set the value of the JSON key item based on the jsonparser value
// type returned.
func setPathway(c *gabs.Container, path string, value []byte, dt jsonparser.ValueType) error {
	var err error

	switch dt {
	case jsonparser.Number:
		{
			v, _ := strconv.Atoi(string(value))
			_, err = c.SetP(v, path)
		}
		break
	case jsonparser.Boolean:
		{
			v, _ := strconv.ParseBool(string(value))
			_, err = c.SetP(v, path)
		}
		break
	default:
		_, err = c.SetP(string(value), path)
	}

	return err
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
		value, dt, err := f.lookupConfigurationValue(v)
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
				if err := setPathway(child, strings.Trim(parts[1], "."), value, dt); err != nil {
					return nil, err
				}
			}
		} else {
			if err = setPathway(parsed, v.Match, value, dt); err != nil {
				return nil, err
			}
		}
	}

	return parsed, nil
}

// Prases a json file updating any matching key/value pairs. If a match is not found, the
// value is set regardless in the file. See the commentary in parseYamlFile for more details
// about what is happening during this process.
func (f *ConfigurationFile) parseJsonFile(path string) error {
	b, err := readFileBytes(path)
	if err != nil {
		return errors.WithStack(err)
	}

	data, err := f.IterateOverJson(b)
	if err != nil {
		return errors.WithStack(err)
	}

	output := []byte(data.StringIndent("", "    "))
	return ioutil.WriteFile(path, output, 0644)
}

// Parses a yaml file and updates any matching key/value pairs before persisting
// it back to the disk.
func (f *ConfigurationFile) parseYamlFile(path string) error {
	b, err := readFileBytes(path)
	if err != nil {
		return errors.WithStack(err)
	}

	// Unmarshal the yaml data into a JSON interface such that we can work with
	// any arbitrary data structure. If we don't do this, I can't use gabs which
	// makes working with unknown JSON signficiantly easier.
	jsonBytes, err := yaml.YAMLToJSON(b)
	if err != nil {
		return errors.WithStack(err)
	}

	// Now that the data is converted, treat it just like JSON and pass it to the
	// iterator function to update values as necessary.
	data, err := f.IterateOverJson(jsonBytes)
	if err != nil {
		return errors.WithStack(err)
	}

	// Remarshal the JSON into YAML format before saving it back to the disk.
	marshaled, err := yaml.JSONToYAML(data.Bytes())
	if err != nil {
		return errors.WithStack(err)
	}

	return ioutil.WriteFile(path, marshaled, 0644)
}

// Parses a text file using basic find and replace. This is a highly inefficient method of
// scanning a file and performing a replacement. You should attempt to use anything other
// than this function where possible.
func (f *ConfigurationFile) parseTextFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return errors.WithStack(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		hasReplaced := false
		t := scanner.Text()

		// Iterate over the potential replacements for the line and check if there are
		// any matches.
		for _, replace := range f.Replace {
			if !strings.HasPrefix(t, replace.Match) {
				continue
			}

			hasReplaced = true
			t = strings.Replace(t, replace.Match, replace.Value, 1)
		}

		// If there was a replacement that occurred on this specific line, do a write to the file
		// immediately to write that modified content to the disk.
		if hasReplaced {
			if _, err := file.WriteAt([]byte(t), int64(len(scanner.Bytes()))); err != nil {
				return errors.WithStack(err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Parses a properties file and updates the values within it to match those that
// are passed. Writes the file once completed.
func (f *ConfigurationFile) parsePropertiesFile(path string) error {
	p, err := properties.LoadFile(path, properties.UTF8)
	if err != nil {
		return errors.WithStack(err)
	}

	for _, replace := range f.Replace {
		data, _, err := f.lookupConfigurationValue(replace)
		if err != nil {
			return errors.WithStack(err)
		}

		if _, _, err := p.Set(replace.Match, string(data)); err != nil {
			return errors.WithStack(err)
		}
	}

	w, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return errors.WithStack(err)
	}

	_, err = p.Write(w, properties.UTF8)

	return err
}

// Looks up a configuration value on the Daemon given a dot-notated syntax.
func (f *ConfigurationFile) lookupConfigurationValue(cfr ConfigurationFileReplacement) ([]byte, jsonparser.ValueType, error) {
	if !configMatchRegex.Match([]byte(cfr.Value)) {
		return []byte(cfr.Value), cfr.ValueType, nil
	}

	// If there is a match, lookup the value in the configuration for the Daemon. If no key
	// is found, just return the string representation, otherwise use the value from the
	// daemon configuration here.
	huntPath := configMatchRegex.ReplaceAllString(cfr.Value, "$1")

	var path []string
	// The camel casing is important here, the configuration for the Daemon does not use
	// JSON, and as such all of the keys will be generated in CamelCase format, rather than
	// the expected snake_case from the old Daemon.
	for _, value := range strings.Split(huntPath, ".") {
		path = append(path, strcase.ToCamel(value))
	}

	// Look for the key in the configuration file, and if found return that value to the
	// calling function.
	match, dt, _, err := jsonparser.Get(f.configuration, path...)
	if err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return match, dt, errors.WithStack(err)
		}

		// If there is no key, keep the original value intact, that way it is obvious there
		// is a replace issue at play.
		return []byte(cfr.Value), cfr.ValueType, nil
	} else {
		return match, cfr.ValueType, nil
	}
}
