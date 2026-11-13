//go:build darwin && arm64

package sweep

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// FindTrash returns the Trash folder that folders under root (already
// resolved and guarded) can be moved into: ~/.Trash when root is on the
// same volume, otherwise the volume's own .Trashes/<uid>, like Finder uses
// for an external drive. It creates the folder if it's missing. A Trash on
// another volume is no use (moving there would mean copying), so that, or
// a Trash that's a symlink or not a folder, is an error.
func FindTrash(root string) (string, error) {
	rfi, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if home, err := os.UserHomeDir(); err == nil {
		if hfi, err := os.Lstat(home); err == nil && hfi.IsDir() && dev(hfi) == dev(rfi) {
			return trashDir(filepath.Join(home, ".Trash"), 0o700, rfi)
		}
	}
	var st unix.Statfs_t
	if err := unix.Statfs(root, &st); err != nil {
		return "", err
	}
	mnt := unix.ByteSliceToString(st.Mntonname[:])
	if !filepath.IsAbs(mnt) {
		return "", fmt.Errorf("can't tell which disk %s is on", root)
	}
	// .Trashes is shared by every user of the disk: anyone can add their
	// own folder, nobody can list or remove anyone else's (d-wx-wx-wt).
	trashes := filepath.Join(mnt, ".Trashes")
	if _, err := trashDir(trashes, os.ModeSticky|0o333, rfi); err != nil {
		return "", err
	}
	return trashDir(filepath.Join(trashes, strconv.Itoa(os.Getuid())), 0o700, rfi)
}

// trashDir makes sure dir is a real folder on the same volume as root,
// creating it with mode if it doesn't exist.
func trashDir(dir string, mode fs.FileMode, root os.FileInfo) (string, error) {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		switch err := os.Mkdir(dir, mode); {
		case err == nil:
			os.Chmod(dir, mode) // Mkdir's mode is cut by the umask
		case !errors.Is(err, fs.ErrExist): // made meanwhile, by Finder perhaps
			return "", err
		}
		fi, err = os.Lstat(dir)
	}
	switch {
	case err != nil:
		return "", err
	case !fi.IsDir(): // a symlink isn't a dir to Lstat
		return "", fmt.Errorf("%s is not a folder", dir)
	case dev(fi) != dev(root):
		return "", fmt.Errorf("%s is on another disk", dir)
	}
	return dir, nil
}

// maxName is the longest file name, in bytes, APFS and HFS+ both take.
const maxName = 255

// trashName is the name the folder at rel (relative to root) gets in the
// Trash: the path it came from, starting with root's own name, with › for
// /, such as "Code › web › node_modules". Finder's Put Back doesn't know
// about folders sweep moved, so the name says where one goes back; and
// starting with root's name keeps a folder like .next visible, where Finder
// would hide it. n > 1 numbers a name that's already taken, like Finder
// does: "Code › web › node_modules 2". A name too long for the disk keeps
// its end, the folder's own name and project.
func trashName(root, rel string, n int) string {
	name := strings.ReplaceAll(filepath.Join(filepath.Base(root), rel), string(filepath.Separator), " › ")
	num := ""
	if n > 1 {
		num = " " + strconv.Itoa(n)
	}
	if len(name)+len(num) > maxName {
		name = "…" + cutStart(name, maxName-len(num)-len("…"))
	}
	return name + num
}

// cutStart shortens s to at most n bytes, dropping whole runes from the start.
func cutStart(s string, n int) string {
	for len(s) > n {
		_, size := utf8.DecodeRuneInString(s)
		s = s[size:]
	}
	return s
}

// toTrash moves name, in the folder parent is open on, into the Trash
// folder trash as trashName(root, rel, n) with the first n that's free.
// The rename starts from parent's open folder, so it moves exactly the
// entry Remove checked, and RENAME_EXCL never replaces anything already in
// the Trash.
func toTrash(parent *os.Root, name, trash, root, rel string) error {
	dir, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	conn, err := dir.SyscallConn()
	if err != nil {
		return err
	}
	for n := 1; n <= 1000; n++ {
		var err error
		dest := filepath.Join(trash, trashName(root, rel, n))
		if cerr := conn.Control(func(fd uintptr) {
			err = unix.RenameatxNp(int(fd), name, unix.AT_FDCWD, dest, unix.RENAME_EXCL)
		}); cerr != nil {
			return cerr
		}
		if !errors.Is(err, unix.EEXIST) {
			if err != nil {
				return &os.LinkError{Op: "move to Trash", Old: filepath.Join(root, rel), New: dest, Err: err}
			}
			return nil
		}
	}
	return fmt.Errorf("move %s to Trash: too many folders with that name in %s", filepath.Join(root, rel), trash)
}
