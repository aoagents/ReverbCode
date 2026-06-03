//go:build unix

package runfile

import "os"

// atomicReplace renames src to dst, replacing dst if it exists. POSIX
// rename(2) is atomic and overwrites an existing destination by default,
// provided src and dst live on the same filesystem — which is always true
// here because the temp file is created in the target directory.
func atomicReplace(src, dst string) error {
	return os.Rename(src, dst)
}
