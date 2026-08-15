package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGuestFSWritesListsReadsAndRenames(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "workspace", "hello.txt")
	if err := runGuestFS([]string{"write", original}, bytes.NewBufferString("hello"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runGuestFS([]string{"append", original}, bytes.NewBufferString(" world"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var read bytes.Buffer
	if err := runGuestFS([]string{"read", original}, bytes.NewReader(nil), &read); err != nil {
		t.Fatal(err)
	}
	if read.String() != "hello world" {
		t.Fatalf("content = %q", read.String())
	}

	var listing bytes.Buffer
	if err := runGuestFS([]string{"list", filepath.Dir(original)}, bytes.NewReader(nil), &listing); err != nil {
		t.Fatal(err)
	}
	var entries []guestFileEntry
	if err := json.Unmarshal(listing.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "hello.txt" || entries[0].Type != "file" {
		t.Fatalf("entries = %#v", entries)
	}

	renamed := filepath.Join(root, "other", "renamed.txt")
	if err := runGuestFS([]string{"rename", original, renamed}, bytes.NewReader(nil), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(renamed); err != nil || string(content) != "hello world" {
		t.Fatalf("renamed content = %q, err = %v", content, err)
	}
}
