package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInlinePathHoistsLocalComponents(t *testing.T) {
	dir := t.TempDir()
	pathFile := filepath.Join(dir, "paths.yaml")
	if err := os.WriteFile(pathFile, []byte(`install:
  get:
    parameters:
      - $ref: '#/components/parameters/SetupTokenHeader'
components:
  parameters:
    SetupTokenHeader:
      name: X-Config-Install-Token
      in: header
      schema:
        $ref: '#/components/schemas/SetupToken'
  schemas:
    SetupToken:
      type: string
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var entry yaml.Node
	if err := yaml.Unmarshal([]byte(`paths:
  /install:
    $ref: './paths.yaml#/install'
`), &entry); err != nil {
		t.Fatal(err)
	}
	b := &bundler{root: entry.Content[0], cache: map[string]*yaml.Node{}, hoisted: map[string]bool{}}
	if err := b.expand(b.root, dir); err != nil {
		t.Fatal(err)
	}
	parameter := navigate(&entry, "/components/parameters/SetupTokenHeader")
	if parameter == nil || lookup(parameter, "name").Value != "X-Config-Install-Token" {
		t.Fatal("inlined path parameter was not hoisted")
	}
	if navigate(&entry, "/components/schemas/SetupToken") == nil {
		t.Fatal("component's local schema reference was not hoisted")
	}
	operation := lookup(lookup(b.root, "paths"), "/install")
	parameters := lookup(lookup(operation, "get"), "parameters")
	if ref, ok := refOf(parameters.Content[0]); !ok || ref != "#/components/parameters/SetupTokenHeader" {
		t.Fatal("local parameter reference changed during inlining")
	}
}
