package config

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// fileConfig is the YAML file's shape. Its yaml tags are the set of known keys.
// A nil field is a key the file does not set.
type fileConfig struct {
	ListenAddr      *string `yaml:"listen_addr"`
	LogLevel        *string `yaml:"log_level"`
	ShutdownTimeout *string `yaml:"shutdown_timeout"`
}

// fileValues returns f's fields by yaml key.
func fileValues(f *fileConfig) map[string]*string {
	v := reflect.ValueOf(f).Elem()
	t := v.Type()
	values := make(map[string]*string, t.NumField())
	for i := range t.NumField() {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		values[key] = v.Field(i).Interface().(*string)
	}
	return values
}

// Load returns the configuration: defaults, then the file, then env (Config steps 1–6
// in specs/000-foundation/plan.md). Every error is an *Error.
func Load(opts Options) (Config, Source, error) {
	cfg := Defaults()
	src := Source{EnvOverrides: []string{}}

	path, data, err := readFile(opts)
	if err != nil {
		return Config{}, Source{}, err
	}
	if path != "" {
		values, lines, err := parseFile(path, data)
		if err != nil {
			return Config{}, Source{}, err
		}
		for _, s := range settings {
			v := values[s.key]
			if v == nil {
				continue
			}
			if reason := s.apply(&cfg, *v); reason != "" {
				return Config{}, Source{}, &Error{Key: s.key, Source: path, Reason: reason, Line: lines[s.key]}
			}
		}
		src.File = path
	}

	if opts.LookupEnv != nil {
		for _, s := range settings {
			name := envName(s.key)
			v, ok := opts.LookupEnv(name)
			// Set but empty counts as unset (ADR 0002).
			if !ok || v == "" {
				continue
			}
			if reason := s.apply(&cfg, v); reason != "" {
				return Config{}, Source{}, &Error{Key: s.key, Source: name, Reason: reason}
			}
			src.EnvOverrides = append(src.EnvOverrides, s.key)
		}
	}
	return cfg, src, nil
}

// readFile picks the file (step 1). It returns "" and no error when there is no file.
func readFile(opts Options) (string, []byte, error) {
	path, explicit := opts.Path, opts.Path != ""
	if !explicit {
		path = opts.DefaultPath
	}
	if path == "" {
		return "", nil, nil
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return path, data, nil
	case errors.Is(err, fs.ErrNotExist) && !explicit:
		return "", nil, nil
	case errors.Is(err, fs.ErrNotExist):
		return "", nil, &Error{Source: path, Reason: reasonFileNotFound}
	default:
		return "", nil, &Error{Source: path, Reason: reasonCannotRead}
	}
}

// syntaxLine matches yaml.v3's syntax errors, "yaml: line N: <problem>". Only N is
// kept: the problem text can quote the value.
var syntaxLine = regexp.MustCompile(`^yaml: line (\d+):`)

// parseFile checks the file's structure and returns its values and the line of each
// key it sets (steps 2–4).
func parseFile(path string, data []byte) (map[string]*string, map[string]int, error) {
	fail := func(key, reason string, line int) error {
		return &Error{Key: key, Source: path, Reason: reason, Line: line}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		line := 0
		if m := syntaxLine.FindStringSubmatch(err.Error()); m != nil {
			line, _ = strconv.Atoi(m[1])
		}
		return nil, nil, fail("", reasonMalformedYAML, line)
	}

	known := fileValues(&fileConfig{})
	lines := map[string]int{}
	if root := documentRoot(&doc); root != nil {
		if root.Kind != yaml.MappingNode {
			return nil, nil, fail("", reasonNotMapping, root.Line)
		}
		for i := 0; i+1 < len(root.Content); i += 2 {
			k, v := root.Content[i], root.Content[i+1]
			if _, ok := known[k.Value]; !ok || k.Kind != yaml.ScalarNode {
				return nil, nil, fail(k.Value, reasonUnknownKey, k.Line)
			}
			// yaml.v3 only checks duplicates when decoding into a map or struct.
			if _, seen := lines[k.Value]; seen {
				return nil, nil, fail(k.Value, reasonDuplicateKey, k.Line)
			}
			if v.Kind != yaml.ScalarNode {
				return nil, nil, fail(k.Value, reasonInvalidType, k.Line)
			}
			lines[k.Value] = k.Line
		}
	}

	// (*Node).Decode has no KnownFields, so decode the raw bytes. After the walk
	// this fails only on a value the struct can't hold, like `!!int abc`. Its
	// message can quote the value, so only the reason is kept.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f fileConfig
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) { // an empty or comment-only file
			return map[string]*string{}, lines, nil
		}
		return nil, nil, fail("", reasonInvalidConfig, 0)
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		return nil, nil, fail("", reasonMultipleDocuments, next.Line)
	}
	return fileValues(&f), lines, nil
}

// documentRoot is the document's top node, or nil for an empty document: an empty
// or comment-only file, or a bare `---` or `~`.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return nil
	}
	return root
}
