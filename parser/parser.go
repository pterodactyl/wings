package parser

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/beevik/etree"
	"github.com/buger/jsonparser"
	"github.com/goccy/go-json"
	"github.com/icza/dyno"
	"github.com/magiconair/properties"
	"gopkg.in/ini.v1"
	"gopkg.in/yaml.v2"

	"github.com/pterodactyl/wings/config"
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

type ReplaceValue struct {
	value     []byte
	valueType jsonparser.ValueType
}

// Value returns the underlying value of the replacement. Be aware that this
// can include escaped UTF-8 sequences that will need to be handled by the caller
// in order to avoid accidentally injecting invalid sequences into the running
// process.
//
// For example the expected value may be "§Foo" but you'll be working directly
// with "\u00a7FOo" for this value. This will cause user pain if not solved since
// that is clearly not the value they were expecting to be using.
func (cv *ReplaceValue) Value() []byte {
	return cv.value
}

// Type returns the underlying data type for the Value field.
func (cv *ReplaceValue) Type() jsonparser.ValueType {
	return cv.valueType
}

// String returns the value as a string representation. This will automatically
// handle casting the UTF-8 sequence into the expected value, switching something
// like "\u00a7Foo" into "§Foo".
func (cv *ReplaceValue) String() string {
	switch cv.Type() {
	case jsonparser.String:
		str, err := jsonparser.ParseString(cv.value)
		if err != nil {
			panic(errors.Wrap(err, "parser: could not parse value"))
		}
		return str
	case jsonparser.Null:
		return "<nil>"
	case jsonparser.Boolean:
		return string(cv.value)
	case jsonparser.Number:
		return string(cv.value)
	default:
		return "<invalid>"
	}
}

type ConfigurationParser string

func (cp ConfigurationParser) String() string {
	return string(cp)
}

// ConfigurationFile defines a configuration file for the server startup. These
// will be looped over and modified before the server finishes booting.
type ConfigurationFile struct {
	FileName string                         `json:"file"`
	Parser   ConfigurationParser            `json:"parser"`
	Replace  []ConfigurationFileReplacement `json:"replace"`

	// Tracks Wings' configuration so that we can quickly get values
	// out of it when variables request it.
	configuration []byte
}

// UnmarshalJSON is a custom unmarshaler for configuration files. If there is an
// error while parsing out the replacements, don't fail the entire operation,
// just log a global warning so someone can find the issue, and return an empty
// array of replacements.
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

// ConfigurationFileReplacement defines a single find/replace instance for a
// given server configuration file.
type ConfigurationFileReplacement struct {
	Match       string       `json:"match"`
	IfValue     string       `json:"if_value"`
	ReplaceWith ReplaceValue `json:"replace_with"`
}

// UnmarshalJSON handles unmarshaling the JSON representation into a struct that
// provides more useful data to this functionality.
func (cfr *ConfigurationFileReplacement) UnmarshalJSON(data []byte) error {
	m, err := jsonparser.GetString(data, "match")
	if err != nil {
		return err
	}

	cfr.Match = m

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

	if errors.Is(err, os.ErrNotExist) {
		// File doesn't exist, we tried creating it, and same error is returned? Pretty
		// sure this pathway is impossible, but if not, abort here.
		if internal {
			return nil
		}

		b := strings.TrimSuffix(path, filepath.Base(path))
		if err := os.MkdirAll(b, 0o755); err != nil {
			return errors.WithMessage(err, "failed to create base directory for missing configuration file")
		} else {
			if _, err := os.Create(path); err != nil {
				return errors.WithMessage(err, "failed to create missing configuration file")
			}
		}

		return f.Parse(path, true)
	}

	return err
}

// Parses an xml file.
func (f *ConfigurationFile) parseXmlFile(path string) error {
	doc := etree.NewDocument()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
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
			element := doc.Root()

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
	// by creating it if not exists. Then, immediately close the file since we will use
	// other methods to write the new contents.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	file.Close()

	cfg, err := ini.Load(path)
	if err != nil {
		return err
	}

	for _, replacement := range f.Replace {
		var (
			path         []string
			bracketDepth int
			v            []int32
		)
		for _, c := range replacement.Match {
			switch c {
			case '[':
				bracketDepth++
			case ']':
				bracketDepth--
			case '.':
				if bracketDepth > 0 || len(path) == 1 {
					v = append(v, c)
					continue
				}
				path = append(path, string(v))
				v = v[:0]
			default:
				v = append(v, c)
			}
		}
		path = append(path, string(v))

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

	return cfg.SaveTo(path)
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
	return os.WriteFile(path, output, 0o644)
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
	// makes working with unknown JSON significantly easier.
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

	return os.WriteFile(path, marshaled, 0o644)
}

// Parses a text file using basic find and replace. This is a highly inefficient method of
// scanning a file and performing a replacement. You should attempt to use anything other
// than this function where possible.
func (f *ConfigurationFile) parseTextFile(path string) error {
	input, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(input), "\n")
	for i, line := range lines {
		for _, replace := range f.Replace {
			// If this line doesn't match what we expect for the replacement, move on to the next
			// line. Otherwise, update the line to have the replacement value.
			if !strings.HasPrefix(line, replace.Match) {
				continue
			}

			lines[i] = replace.ReplaceWith.String()
		}
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return err
	}

	return nil
}

// parsePropertiesFile parses a properties file and updates the values within it
// to match those that are passed. Once completed the new file is written to the
// disk. This will cause comments not present at the head of the file to be
// removed unfortunately.
//
// Any UTF-8 value will be written back to the disk as their escaped value rather
// than the raw value There is no winning with this logic. This fixes a bug where
// users with hand rolled UTF-8 escape sequences would have all sorts of pain in
// their configurations because we were writing the UTF-8 literal characters which
// their games could not actually handle.
//
// However, by adding this fix to only store the escaped UTF-8 sequence we
// unwittingly introduced a "regression" that causes _other_ games to have issues
// because they can only handle the unescaped representations. I cannot think of
// a simple approach to this problem that doesn't just lead to more complicated
// cases and problems.
//
// So, if your game cannot handle parsing UTF-8 sequences that are escaped into
// the string, well, sucks. There are fewer of those games than there are games
// that have issues parsing the raw UTF-8 sequence into a string? Also how does
// one really know what the user intended at this point? We'd need to know if
// the value was escaped or not to begin with before setting it, which I suppose
// can work but jesus that is going to be some annoyingly complicated logic?
//
// @see https://github.com/pterodactyl/panel/issues/2308 (original)
// @see https://github.com/pterodactyl/panel/issues/3009 ("bug" introduced as result)
func (f *ConfigurationFile) parsePropertiesFile(path string) error {
	var s strings.Builder
	// Open the file and attempt to load any comments that currenty exist at the start
	// of the file. This is kind of a hack, but should work for a majority of users for
	// the time being.
	if fd, err := os.Open(path); err != nil {
		return errors.Wrap(err, "parser: could not open file for reading")
	} else {
		scanner := bufio.NewScanner(fd)
		// Scan until we hit a line that is not a comment that actually has content
		// on it. Keep appending the comments until that time.
		for scanner.Scan() {
			text := scanner.Text()
			if len(text) > 0 && text[0] != '#' {
				break
			}
			s.WriteString(text + "\n")
		}
		_ = fd.Close()
		if err := scanner.Err(); err != nil {
			return errors.WithStackIf(err)
		}
	}

	p, err := properties.LoadFile(path, properties.UTF8)
	if err != nil {
		return errors.Wrap(err, "parser: could not load properties file for configuration update")
	}

	// Replace any values that need to be replaced.
	for _, replace := range f.Replace {
		data, err := f.LookupConfigurationValue(replace)
		if err != nil {
			return errors.Wrap(err, "parser: failed to lookup configuration value")
		}

		v, ok := p.Get(replace.Match)
		// Don't attempt to replace the value if we're looking for a specific value and
		// it does not match. If there was no match at all in the file for this key but
		// we're doing an IfValue match, do nothing.
		if replace.IfValue != "" && (!ok || (ok && v != replace.IfValue)) {
			continue
		}

		if _, _, err := p.Set(replace.Match, data); err != nil {
			return errors.Wrap(err, "parser: failed to set replacement value")
		}
	}

	// Add the new file content to the string builder.
	for _, key := range p.Keys() {
		value, ok := p.Get(key)
		if !ok {
			continue
		}
		// This escape is intentional!
		//
		// See the docblock for this function for more details, do not change this
		// or you'll cause a flood of new issue reports no one wants to deal with.
		s.WriteString(key + "=" + strings.Trim(strconv.QuoteToASCII(value), "\"") + "\n")
	}

	// Open the file for writing.
	w, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer w.Close()

	// Write the data to the file.
	if _, err := w.Write([]byte(s.String())); err != nil {
		return errors.Wrap(err, "parser: failed to write properties file to disk")
	}

	return nil
}
