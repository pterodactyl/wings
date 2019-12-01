package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/buger/jsonparser"
	"github.com/magiconair/properties"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"regexp"
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
	}

	return err
}

// Parses a yaml file and updates any matching key/value pairs before persisting
// it back to the disk.
func (f *ConfigurationFile) parseYamlFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return errors.WithStack(err)
	}
	defer file.Close()

	b, err := ioutil.ReadAll(file)
	if err != nil {
		return errors.WithStack(err)
	}

	var raw interface{}
	// Unmarshall the yaml data into a raw interface such that we can work with any arbitrary
	// data structure.
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return errors.WithStack(err)
	}

	// Create an indexable map that we can use while looping through elements.
	m := raw.(map[interface{}]interface{})

	for _, v := range f.Replace {
		value, err := lookupConfigurationValue(v.Value)
		if err != nil {
			return errors.WithStack(err)
		}

		layer := m
		nest := strings.Split(v.Match, ".")

		// Split the key name on any periods, as we do this, initalize the struct for the yaml
		// data at that key and then reset the later to point to that newly created layer. If
		// we have reached the last split item, set the value of the key to the value defined
		// in the replacement data.
		for i, key := range nest {
			if i == (len(nest) - 1) {
				layer[key] = value
			} else {
				// Don't overwrite the key if it exists in the data already. But, if it is missing,
				// go ahead and create the key otherwise we'll hit a panic when trying to access an
				// index that does not exist.
				if m[key] == nil {
					layer[key] = make(map[interface{}]interface{})
				}

				layer = m[key].(map[interface{}]interface{})
			}
		}
	}

	file.Close()

	if o, err := yaml.Marshal(m); err != nil {
		return errors.WithStack(err)
	} else {
		return ioutil.WriteFile(path, o, 0644)
	}
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
		v, err := lookupConfigurationValue(replace.Value)
		if err != nil {
			return errors.WithStack(err)
		}

		if _, _, err := p.Set(replace.Match, v); err != nil {
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
func lookupConfigurationValue(value string) (string, error) {
	// @todo there is probably a much better way to handle this
	mb, _ := json.Marshal(config.Get())

	if !configMatchRegex.Match([]byte(value)) {
		return value, nil
	}

	// If there is a match, lookup the value in the configuration for the Daemon. If no key
	// is found, just return the string representation, otherwise use the value from the
	// daemon configuration here.
	v := configMatchRegex.ReplaceAllString(value, "$1")

	match, err := jsonparser.GetString(mb, strings.Split(v, ".")...)
	if err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return "", errors.WithStack(err)
		}

		// If there is no key, keep the original value intact, that way it is obvious there
		// is a replace issue at play.
		v = value
	} else {
		v = match
	}

	return v, nil
}
