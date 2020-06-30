package parser

import (
	"bufio"
	"encoding/json"
	"github.com/apex/log"
	"github.com/beevik/etree"
	"github.com/buger/jsonparser"
	"github.com/icza/dyno"
	"github.com/magiconair/properties"
	"github.com/pkg/errors"
	"github.com/pterodactyl/wings/config"
	"gopkg.in/ini.v1"
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

type ConfigurationParser string

func (cp ConfigurationParser) String() string {
	return string(cp)
}

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

// Custom unmarshaler for configuration files. If there is an error while parsing out the
// replacements, don't fail the entire operation, just log a global warning so someone can
// find the issue, and return an empty array of replacements.
//
// I imagine people will notice configuration replacement isn't working correctly and then
// the logs should help better expose that issue.
func (f *ConfigurationFile) UnmarshalJSON(data []byte) error {
	var m map[string]*json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	if err := json.Unmarshal(*m["file"], &f.FileName); err != nil {
		return err
	}

	if err := json.Unmarshal(*m["parser"], &f.Parser); err != nil {
		return err
	}

	if err := json.Unmarshal(*m["replace"], &f.Replace); err != nil {
		log.WithField("file", f.FileName).WithField("error", err).Warn("failed to unmarshal configuration file replacement")

		f.Replace = []ConfigurationFileReplacement{}
	}

	return nil
}

// Regex to match paths such as foo[1].bar[2] and convert them into a format that
// gabs can work with, such as foo.1.bar.2 in this case. This is applied when creating
// the struct for the configuration file replacements.
var cfrMatchReplacement = regexp.MustCompile(`\[(\d+)]`)

// Defines a single find/replace instance for a given server configuration file.
type ConfigurationFileReplacement struct {
	Match       string       `json:"match"`
	IfValue     string       `json:"if_value"`
	ReplaceWith ReplaceValue `json:"replace_with"`
}

// Handles unmarshaling the JSON representation into a struct that provides more useful
// data to this functionality.
func (cfr *ConfigurationFileReplacement) UnmarshalJSON(data []byte) error {
	m, err := jsonparser.GetString(data, "match")
	if err != nil {
		return err
	}

	// See comment on the replacement regex to understand what exactly this is doing.
	cfr.Match = cfrMatchReplacement.ReplaceAllString(m, ".$1")

	iv, err := jsonparser.GetString(data, "if_value")
	// We only check keypath here since match & replace_with should be present on all of
	// them, however if_value is optional.
	if err != nil && err != jsonparser.KeyPathNotFoundError {
		return err
	}
	cfr.IfValue = iv

	rw, dt, _, err := jsonparser.Get(data, "replace_with")
	if err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return err
		}

		// Okay, likely dealing with someone who forgot to upgrade their eggs, so in
		// that case, fallback to using the old key which was "value".
		rw, dt, _, err = jsonparser.Get(data, "value")
		if err != nil {
			return err
		}
	}

	cfr.ReplaceWith = ReplaceValue{
		value:     rw,
		valueType: dt,
	}

	return nil
}

// Parses a given configuration file and updates all of the values within as defined
// in the API response from the Panel.
func (f *ConfigurationFile) Parse(path string, internal bool) error {
	log.WithField("path", path).WithField("parser", f.Parser.String()).Debug("parsing server configuration file")

	if mb, err := json.Marshal(config.Get()); err != nil {
		return err
	} else {
		f.configuration = mb
	}

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
	case Ini:
		err = f.parseIniFile(path)
		break
	case Xml:
		err = f.parseXmlFile(path)
		break
	}

	if os.IsNotExist(err) {
		// File doesn't exist, we tried creating it, and same error is returned? Pretty
		// sure this pathway is impossible, but if not, abort here.
		if internal {
			return nil
		}

		if _, err := os.Create(path); err != nil {
			return errors.WithStack(err)
		}

		return f.Parse(path, true)
	}

	return errors.WithStack(err)
}

// Parses an xml file.
func (f *ConfigurationFile) parseXmlFile(path string) error {
	doc := etree.NewDocument()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := doc.ReadFrom(file); err != nil {
		return err
	}

	// If there is no root we should create a basic start to the file. This isn't required though,
	// and if it doesn't work correctly I'll just remove the code.
	if doc.Root() == nil {
		doc.CreateProcInst("xml", `version="1.0" encoding="utf-8"`)
	}

	for i, replacement := range f.Replace {
		value, err := f.LookupConfigurationValue(replacement)
		if err != nil {
			return err
		}

		// If this is the first item and there is no root element, create that root now and apply
		// it for future use.
		if i == 0 && doc.Root() == nil {
			parts := strings.SplitN(replacement.Match, ".", 2)
			doc.SetRoot(doc.CreateElement(parts[0]))
		}

		path := "./" + strings.Replace(replacement.Match, ".", "/", -1)

		// If we're not doing a wildcard replacement go ahead and create the
		// missing element if we cannot find it yet.
		if !strings.Contains(path, "*") {
			parts := strings.Split(replacement.Match, ".")

			// Set the initial element to be the root element, and then work from there.
			var element = doc.Root()

			// Iterate over the path to create the required structure for the given element's path.
			// This does not set a value, only ensures that the base structure exists. We start at index
			// 1 because an XML document can only contain a single root element, and from there we'll
			// work our way down the chain.
			for _, tag := range parts[1:] {
				if e := element.FindElement(tag); e == nil {
					element = element.CreateElement(tag)
				} else {
					element = e
				}
			}
		}

		// Iterate over the elements we found and update their values.
		for _, element := range doc.FindElements(path) {
			if xmlValueMatchRegex.MatchString(value) {
				k := xmlValueMatchRegex.ReplaceAllString(value, "$1")
				v := xmlValueMatchRegex.ReplaceAllString(value, "$2")

				element.CreateAttr(k, v)
			} else {
				element.SetText(value)
			}
		}
	}

	// If you don't truncate the file you'll end up duplicating the data in there (or just appending
	// to the end of the file. We don't want to do that.
	if err := file.Truncate(0); err != nil {
		return err
	}

	// Move the cursor to the start of the file to avoid weird spacing issues.
	file.Seek(0, 0)

	// Ensure the XML is indented properly.
	doc.Indent(2)

	// Truncate the file before attempting to write the changes.
	if err := os.Truncate(path, 0); err != nil {
		return err
	}

	// Write the XML to the file.
	_, err = doc.WriteTo(file)

	return err
}

// Parses an ini file.
func (f *ConfigurationFile) parseIniFile(path string) error {
	// Ini package can't handle a non-existent file, so handle that automatically here
	// by creating it if not exists.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	cfg, err := ini.Load(path)
	if err != nil {
		return err
	}

	for _, replacement := range f.Replace {
		path := strings.SplitN(replacement.Match, ".", 2)

		value, err := f.LookupConfigurationValue(replacement)
		if err != nil {
			return err
		}

		k := path[0]
		s := cfg.Section("")
		// Passing a key of foo.bar will look for "bar" in the "[foo]" section of the file.
		if len(path) == 2 {
			k = path[1]
			s = cfg.Section(path[0])
		}

		// If no section was found, create that new section now and then set the
		// section value we're using to be the new one.
		if s == nil {
			s, err = cfg.NewSection(path[0])
			if err != nil {
				return err
			}
		}

		// If the key exists in the file go ahead and set the value, otherwise try to
		// create it in the section.
		if s.HasKey(k) {
			s.Key(k).SetValue(value)
		} else {
			if _, err := s.NewKey(k, value); err != nil {
				return err
			}
		}
	}

	// Truncate the file before attempting to write the changes.
	if err := os.Truncate(path, 0); err != nil {
		return err
	}

	if _, err := cfg.WriteTo(file); err != nil {
		return err
	}

	return nil
}

// Parses a json file updating any matching key/value pairs. If a match is not found, the
// value is set regardless in the file. See the commentary in parseYamlFile for more details
// about what is happening during this process.
func (f *ConfigurationFile) parseJsonFile(path string) error {
	b, err := readFileBytes(path)
	if err != nil {
		return err
	}

	data, err := f.IterateOverJson(b)
	if err != nil {
		return err
	}

	output := []byte(data.StringIndent("", "    "))
	return ioutil.WriteFile(path, output, 0644)
}

// Parses a yaml file and updates any matching key/value pairs before persisting
// it back to the disk.
func (f *ConfigurationFile) parseYamlFile(path string) error {
	b, err := readFileBytes(path)
	if err != nil {
		return err
	}

	i := make(map[string]interface{})
	if err := yaml.Unmarshal(b, &i); err != nil {
		return err
	}

	// Unmarshal the yaml data into a JSON interface such that we can work with
	// any arbitrary data structure. If we don't do this, I can't use gabs which
	// makes working with unknown JSON signficiantly easier.
	jsonBytes, err := json.Marshal(dyno.ConvertMapI2MapS(i))
	if err != nil {
		return err
	}

	// Now that the data is converted, treat it just like JSON and pass it to the
	// iterator function to update values as necessary.
	data, err := f.IterateOverJson(jsonBytes)
	if err != nil {
		return err
	}

	// Remarshal the JSON into YAML format before saving it back to the disk.
	marshaled, err := yaml.Marshal(data.Data())
	if err != nil {
		return err
	}

	return ioutil.WriteFile(path, marshaled, 0644)
}

// Parses a text file using basic find and replace. This is a highly inefficient method of
// scanning a file and performing a replacement. You should attempt to use anything other
// than this function where possible.
func (f *ConfigurationFile) parseTextFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
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
			t = strings.Replace(t, replace.Match, replace.ReplaceWith.String(), 1)
		}

		// If there was a replacement that occurred on this specific line, do a write to the file
		// immediately to write that modified content to the disk.
		if hasReplaced {
			if _, err := file.WriteAt([]byte(t), int64(len(scanner.Bytes()))); err != nil {
				return err
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

// Parses a properties file and updates the values within it to match those that
// are passed. Writes the file once completed.
func (f *ConfigurationFile) parsePropertiesFile(path string) error {
	p, err := properties.LoadFile(path, properties.UTF8)
	if err != nil {
		return err
	}

	for _, replace := range f.Replace {
		data, err := f.LookupConfigurationValue(replace)
		if err != nil {
			return err
		}

		v, ok := p.Get(replace.Match)
		// Don't attempt to replace the value if we're looking for a specific value and
		// it does not match. If there was no match at all in the file for this key but
		// we're doing an IfValue match, do nothing.
		if replace.IfValue != "" && (!ok || (ok && v != replace.IfValue)) {
			continue
		}

		if _, _, err := p.Set(replace.Match, data); err != nil {
			return err
		}
	}

	w, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	_, err = p.Write(w, properties.UTF8)

	return err
}
