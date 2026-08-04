//go:build !windows

package fs

func mapUid0ToCurrentUser() int {
	return 0
}
