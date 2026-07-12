package ui

import "testing"

func TestCleanLogLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain text unchanged",
			in:   "hello world",
			want: "hello world",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "carriage return stripped",
			in:   "partial\roverwrite",
			want: "partialoverwrite",
		},
		{
			name: "cursor escape sequence stripped",
			in:   "prefix\x1b[2Kclear",
			want: "prefixclear",
		},
		{
			name: "cursor move up stripped",
			in:   "line1\x1b[1Aline2",
			want: "line1line2",
		},
		{
			name: "cursor move forward stripped",
			in:   "hello\x1b[5Cworld",
			want: "helloworld",
		},
		{
			name: "SGR bold preserved",
			in:   "\x1b[1mBold Text\x1b[0m normal",
			want: "\x1b[1mBold Text\x1b[0m normal",
		},
		{
			name: "SGR color preserved",
			in:   "\x1b[32mGreen\x1b[0m plain",
			want: "\x1b[32mGreen\x1b[0m plain",
		},
		{
			name: "SGR 256-color preserved",
			in:   "\x1b[38;5;82mLime\x1b[0m",
			want: "\x1b[38;5;82mLime\x1b[0m",
		},
		{
			name: "erase line then real content",
			in:   "\r\x1b[33mwarning: foo\x1b[0m",
			want: "\x1b[33mwarning: foo\x1b[0m",
		},
		{
			name: "mixed cursor and SGR",
			in:   "\x1b[2K\x1b[1mCompiling\x1b[0m...",
			want: "\x1b[1mCompiling\x1b[0m...",
		},
		{
			name: "OS command sequence stripped",
			in:   "\x1b]0;title\x07visible",
			want: "visible",
		},
		{
			name: "tab stripped as control char",
			in:   "col1\tcol2",
			want: "col1col2",
		},
		{
			name: "backspace stripped",
			in:   "abc\x08def",
			want: "abcdef",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanLogLine(tt.in)
			if got != tt.want {
				t.Errorf("CleanLogLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
