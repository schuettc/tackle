package apply

import "fmt"

// shellSplit splits a POSIX shell command line produced by command.go into its
// words, honouring single and double quotes (the only quoting shellQuote emits).
// It is not a general shell parser: it covers exactly the commands casebook
// generates so the lane runner can turn a stored command back into (dir, args).
func shellSplit(s string) ([]string, error) {
	var words []string
	var cur []byte
	inWord := false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, string(cur))
				cur = cur[:0]
				inWord = false
			}
			i++
		case c == '\'':
			inWord = true
			i++
			for i < len(s) && s[i] != '\'' {
				cur = append(cur, s[i])
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated single quote in %q", s)
			}
			i++ // closing quote
		case c == '"':
			inWord = true
			i++
			for i < len(s) && s[i] != '"' {
				cur = append(cur, s[i])
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated double quote in %q", s)
			}
			i++ // closing quote
		default:
			inWord = true
			cur = append(cur, c)
			i++
		}
	}
	if inWord {
		words = append(words, string(cur))
	}
	return words, nil
}

// gitCommand parses a "git -C <dir> <args...>" command into its working
// directory and the argument list to pass to RunGit.
func gitCommand(cmd string) (dir string, args []string, err error) {
	w, err := shellSplit(cmd)
	if err != nil {
		return "", nil, err
	}
	if len(w) < 4 || w[0] != "git" || w[1] != "-C" {
		return "", nil, fmt.Errorf("not a git -C command: %q", cmd)
	}
	return w[2], w[3:], nil
}

// parseRemoteDelete parses "git -C <dir> push <remote> --delete <branch>".
func parseRemoteDelete(cmd string) (dir, remote, branch string, err error) {
	d, args, err := gitCommand(cmd)
	if err != nil {
		return "", "", "", err
	}
	if len(args) != 4 || args[0] != "push" || args[2] != "--delete" {
		return "", "", "", fmt.Errorf("not a remote-delete command: %q", cmd)
	}
	return d, args[1], args[3], nil
}

// parseWorktreeRemove parses "git -C <dir> worktree remove <path>".
func parseWorktreeRemove(cmd string) (dir, path string, err error) {
	d, args, err := gitCommand(cmd)
	if err != nil {
		return "", "", err
	}
	if len(args) != 3 || args[0] != "worktree" || args[1] != "remove" {
		return "", "", fmt.Errorf("not a worktree-remove command: %q", cmd)
	}
	return d, args[2], nil
}
