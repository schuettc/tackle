package apply

// SetTempFile replaces how apply writes its temporary files, for the
// external tests; the returned func restores it.
func SetTempFile(f func(content string) (string, error)) func() {
	old := tempFile
	tempFile = f
	return func() { tempFile = old }
}

// WriteTemp is the real one.
var WriteTemp = writeTemp
