package contract

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Load parses a Contract from YAML. Unknown fields are an error: a contract
// with a misspelled key would otherwise silently lose whatever that key
// described.
func Load(r io.Reader) (*Contract, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var c Contract
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("contract.Load: %w", err)
	}
	return &c, nil
}

// Save writes the contract as block-style YAML with two-space indentation.
func (c *Contract) Save(w io.Writer) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("contract.Save: %w", err)
	}
	return enc.Close()
}
