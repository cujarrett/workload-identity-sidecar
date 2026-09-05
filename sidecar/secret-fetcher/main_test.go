package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    map[string]string
		wantErr string
	}{
		{
			name:  "every property becomes a file",
			value: `{"API_TOKEN":"t","API_SECRET":"s"}`,
			want:  map[string]string{"API_TOKEN": "t", "API_SECRET": "s"},
		},
		{
			name:    "placeholder means the owner has not set a value",
			value:   `{"__unset__":"true"}`,
			wantErr: "owner has not set one yet",
		},
		{
			name:    "an empty object is not a value",
			value:   `{}`,
			wantErr: "holds no properties",
		},
		{
			name:    "value is not JSON at all",
			value:   "just-a-string",
			wantErr: "not a flat JSON object",
		},
		{
			name:    "nested JSON cannot map onto files",
			value:   `{"API_TOKEN":{"nested":"x"}}`,
			wantErr: "not a flat JSON object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse(tt.value)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Errorf("key %q: got %q, want %q", k, got[k], want)
				}
			}
		})
	}
}

func TestWriteFileIsAtomicAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "API_TOKEN")

	if err := writeFile(path, "value"); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != "value" {
		t.Errorf("got %q, want %q", got, "value")
	}

	// The temp file must not survive, or a directory listing shows a half-written twin.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file was left behind")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o400 {
		t.Errorf("permissions are %o, want 400 - a secret must not be group or world readable", perm)
	}
}

func TestWriteFileOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "API_TOKEN")

	// A rotated value has to replace the old one, and the first write leaves the file
	// read-only, so this fails if the rename is ever swapped for a plain write.
	if err := writeFile(path, "first"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeFile(path, "second"); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("got %q, want %q", got, "second")
	}
}

func TestLoadBindings(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"role-arn":  "arn:aws:iam::000000000000:role/crossplane/example\n",
		"secret-id": "platform/foo/orders\n",
		"region":    "us-east-1\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
	}

	// The empty entry covers SECRET_BINDINGS being unset or trailing a comma.
	got, err := loadBindings([]string{dir + ":/secrets/orders", "", "  "})
	if err != nil {
		t.Fatalf("loadBindings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d bindings, want 1", len(got))
	}

	b := got[0]
	if b.SecretID != "platform/foo/orders" {
		t.Errorf("secret id %q still carries whitespace", b.SecretID)
	}
	if b.Region != "us-east-1" {
		t.Errorf("region %q still carries whitespace", b.Region)
	}
	if b.OutDir != "/secrets/orders" {
		t.Errorf("output dir parsed as %q", b.OutDir)
	}
}

func TestLoadBindingsMissingFile(t *testing.T) {
	// A binding volume that has not synced yet must be an error rather than a binding
	// with empty fields, which would fail later as a confusing AWS error.
	if _, err := loadBindings([]string{t.TempDir() + ":/secrets/orders"}); err == nil {
		t.Fatal("expected an error for a binding directory with no files")
	}
}

func TestLoadBindingsMalformedPair(t *testing.T) {
	// Without the output dir the fetcher would write values into the read-only Secret
	// volume, so refuse rather than fail confusingly at the first write.
	if _, err := loadBindings([]string{"/bindings/managed-secret-orders"}); err == nil {
		t.Fatal("expected an error for an entry with no output dir")
	}
}
