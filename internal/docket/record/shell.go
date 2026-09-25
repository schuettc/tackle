// Package record turns a harness's report of a shell command into journal
// actions. It keeps verbs, targets and allow-listed flags; it never keeps the
// command line, messages, titles, bodies, field values or credentials.
package record

import "strings"

// Split breaks a shell command line into simple commands (on ;, &, &&, ||,
// |, newlines and parentheses), each a list of words with quotes removed.
// Command substitutions are kept as single opaque words. Comments are
// dropped. It is best effort and never fails.
func Split(cmd string) [][]string {
	var (
		out   [][]string
		words []string
		cur   strings.Builder
		inW   bool
	)
	endWord := func() {
		if inW {
			words = append(words, cur.String())
			cur.Reset()
			inW = false
		}
	}
	endCmd := func() {
		endWord()
		if len(words) > 0 {
			out = append(out, words)
			words = nil
		}
	}
	r := []rune(cmd)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\'':
			inW = true
			j := i + 1
			for j < len(r) && r[j] != '\'' {
				cur.WriteRune(r[j])
				j++
			}
			i = j
		case c == '"':
			inW = true
			j := i + 1
			for j < len(r) && r[j] != '"' {
				if r[j] == '\\' && j+1 < len(r) && strings.ContainsRune("\"\\$`", r[j+1]) {
					j++
				}
				cur.WriteRune(r[j])
				j++
			}
			i = j
		case c == '\\' && i+1 < len(r):
			inW = true
			i++
			if r[i] != '\n' {
				cur.WriteRune(r[i])
			}
		case c == '$' && i+1 < len(r) && r[i+1] == '(':
			inW = true
			depth := 0
			j := i
			for ; j < len(r); j++ {
				cur.WriteRune(r[j])
				if r[j] == '(' {
					depth++
				} else if r[j] == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			i = j
		case c == '#' && !inW:
			for i < len(r) && r[i] != '\n' {
				i++
			}
			endCmd()
		case c == ' ' || c == '\t':
			endWord()
		case c == '\n' || c == ';' || c == '&' || c == '|' || c == '(' || c == ')':
			endCmd()
		default:
			inW = true
			cur.WriteRune(c)
		}
	}
	endCmd()
	return out
}
