package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// quotedValue matches the back-quoted input value that the YAML library puts
// in its type errors, for example "cannot unmarshal !!str `abc` into int".
var quotedValue = regexp.MustCompile("`[^`]*`")

// decodeStrict decodes one YAML document from data into out. Unknown fields,
// repeated keys, an empty file and a second document are all refused. Error
// text from the YAML library has any quoted input value removed, so a secret
// written in the wrong place is never repeated in an error or a log.
func decodeStrict(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("the file is empty")
		}
		return errors.New(scrub(err.Error()))
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("the file holds more than one YAML document")
	}
	return nil
}

// scrub removes quoted input values from a YAML error message.
func scrub(msg string) string {
	return quotedValue.ReplaceAllString(msg, "(value hidden)")
}

// scalarText returns the text of a YAML scalar node, refusing anything else.
// Reading the node's text, rather than letting the YAML library convert it,
// keeps a decimal such as 0.10 exact: it never passes through a float.
func scalarText(node *yaml.Node) (string, error) {
	if node.Kind == yaml.AliasNode {
		return "", fmt.Errorf("line %d: aliases are not allowed here", node.Line)
	}
	if node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("line %d: expected a single value, not a list or a mapping", node.Line)
	}
	return node.Value, nil
}
