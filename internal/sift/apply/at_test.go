package apply

import "os"

// writeAt and removeAt write under the directory root through an os.Root,
// as apply does on its worktree.
func writeAt(root, rel, content string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return writeFile(r, rel, content)
}

func removeAt(root, rel string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return removeFile(r, rel)
}
