package version

import (
	"testing"
)

func TestPathVersion(t *testing.T) {
	tests := []struct {
		name           string
		override       string
		wantVersion    string
		wantPathFilled bool
	}{
		{
			name:           "uses build metadata defaults when override is empty",
			wantPathFilled: true,
		},
		{
			name:           "prefers override",
			override:       "test-version",
			wantVersion:    "test-version",
			wantPathFilled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := VersionOverride
			VersionOverride = test.override

			t.Cleanup(func() {
				VersionOverride = original
			})

			path, gotVersion := PathVersion()
			if test.wantPathFilled && path == "" {
				t.Fatal("path = empty, want non-empty")
			}

			if test.wantVersion != "" && gotVersion != test.wantVersion {
				t.Fatalf("version = %q, want %q", gotVersion, test.wantVersion)
			}

			if test.wantVersion == "" && gotVersion == "" {
				t.Fatal("version = empty, want non-empty")
			}
		})
	}
}
