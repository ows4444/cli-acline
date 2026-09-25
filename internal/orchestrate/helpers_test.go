package orchestrate

import "os"

func osWriteFile(path string) error { return os.WriteFile(path, []byte("stop"), 0o600) }
