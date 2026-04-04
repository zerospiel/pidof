package process

import (
	"slices"
	"testing"
)

func Test_compileQueries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []string
		want  []query
	}{
		{
			name: "normalizes and deduplicates",
			input: []string{
				" bash ",
				"/usr/bin/bash",
				"/usr/bin/bash/",
				"",
				"bash",
				"/tmp/demo (deleted)",
			},
			want: []query{
				{raw: "bash", base: "bash"},
				{raw: "/usr/bin/bash", base: "bash", fullPath: true},
				{raw: "/tmp/demo", base: "demo", fullPath: true},
			},
		},
		{
			name:  "drops empty input",
			input: []string{"", " ", "/"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := compileQueries(test.input)
			if !slices.Equal(got, test.want) {
				t.Fatalf("compileQueries() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func Test_baseName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "whitespace", in: "   ", want: ""},
		{name: "root", in: "/", want: ""},
		{name: "absolute path", in: "/usr/bin/bash", want: "bash"},
		{name: "path with trailing slash", in: "/usr/bin/bash/", want: "bash"},
		{name: "deleted suffix", in: "/tmp/demo (deleted)", want: "demo"},
		{name: "plain name", in: "python3", want: "python3"},
		{name: "relative path", in: "./relative/script.sh", want: "script.sh"},
		{name: "parent relative path", in: "../other/script.sh  ", want: "script.sh"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := baseName(test.in); got != test.want {
				t.Fatalf("baseName(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func Test_nextNULField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []byte
		wantField string
		wantRest  string
		wantOK    bool
	}{
		{
			name:      "skips leading nul bytes",
			input:     []byte{0, 'a', 'b', 0, 'c', 0},
			wantField: "ab",
			wantRest:  "c\x00",
			wantOK:    true,
		},
		{
			name:   "empty after leading nul bytes",
			input:  []byte{0, 0},
			wantOK: false,
		},
		{
			name:      "unterminated field",
			input:     []byte("abc"),
			wantField: "abc",
			wantOK:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			field, rest, ok := nextNULField(test.input)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}

			if string(field) != test.wantField {
				t.Fatalf("field = %q, want %q", field, test.wantField)
			}

			if string(rest) != test.wantRest {
				t.Fatalf("rest = %q, want %q", rest, test.wantRest)
			}
		})
	}
}

func Test_samePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lhs  string
		rhs  string
		want bool
	}{
		{
			name: "matches deleted suffix form",
			lhs:  "/tmp/demo (deleted)",
			rhs:  "/tmp/demo",
			want: true,
		},
		{
			name: "rejects empty lhs",
			lhs:  "",
			rhs:  "/tmp/demo",
			want: false,
		},
		{
			name: "rejects different paths",
			lhs:  "/tmp/demo",
			rhs:  "/tmp/other",
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := samePath(test.lhs, test.rhs); got != test.want {
				t.Fatalf("samePath(%q, %q) = %v, want %v", test.lhs, test.rhs, got, test.want)
			}
		})
	}
}

func Test_firstScriptArg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		argv0 string
		rest  []string
		want  string
	}{
		{
			name:  "shell script path",
			argv0: "/bin/sh",
			rest:  []string{"./tool.sh", "--verbose"},
			want:  "./tool.sh",
		},
		{
			name:  "shell command string is not a script",
			argv0: "/bin/bash",
			rest:  []string{"-c", "echo ready; sleep 20", "shell-name"},
			want:  "",
		},
		{
			name:  "shell grouped command option is not a script",
			argv0: "/bin/sh",
			rest:  []string{"-xc", "echo ready; sleep 20"},
			want:  "",
		},
		{
			name:  "python module mode is not a script",
			argv0: "/usr/bin/python3",
			rest:  []string{"-m", "http.server", "8000"},
			want:  "",
		},
		{
			name:  "python grouped module option is not a script",
			argv0: "python3",
			rest:  []string{"-Im", "http.server"},
			want:  "",
		},
		{
			name:  "python script path",
			argv0: "python3",
			rest:  []string{"tool.py", "--port", "8000"},
			want:  "tool.py",
		},
		{
			name:  "double dash allows dashed script names",
			argv0: "/bin/sh",
			rest:  []string{"--", "-tool.sh"},
			want:  "-tool.sh",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := firstScriptArg(test.argv0, joinNULFields(test.rest...)); got != test.want {
				t.Fatalf("firstScriptArg(%q, %v) = %q, want %q", test.argv0, test.rest, got, test.want)
			}
		})
	}
}

func Test_firstScriptArgN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		argv0     string
		rest      []string
		maxFields int
		want      string
	}{
		{
			name:      "stops before environment after option only argv",
			argv0:     "/bin/bash",
			rest:      []string{"-l", "HOME=/tmp/demo"},
			maxFields: 1,
			want:      "",
		},
		{
			name:      "still finds script within bounded argv",
			argv0:     "/usr/bin/python3",
			rest:      []string{"-I", "tool.py", "HOME=/tmp/demo"},
			maxFields: 2,
			want:      "tool.py",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := firstScriptArgN(test.argv0, joinNULFields(test.rest...), test.maxFields); got != test.want {
				t.Fatalf(
					"firstScriptArgN(%q, %v, %d) = %q, want %q",
					test.argv0,
					test.rest,
					test.maxFields,
					got,
					test.want,
				)
			}
		})
	}
}

func joinNULFields(fields ...string) []byte {
	var joined []byte

	for _, field := range fields {
		joined = append(joined, field...)
		joined = append(joined, 0)
	}

	return joined
}
