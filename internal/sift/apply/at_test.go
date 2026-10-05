package apply

import "os"

// writeAt writes under the directory root through an os.Root, as apply
// does on its worktree.
func writeAt(root, rel, content string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return writeFile(r, rel, content)
}
