package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func exists(p string) bool {
	_, err := os.Stat(p)

	return err == nil
}

func waitForFunctionFSEndpoints(root string, timeout time.Duration) (in0, out0, in1, out1 string, err error) {
	deadline := time.Now().Add(timeout)

	ep := func(n string) string { return filepath.Join(root, n) }
	want := []string{ep("ep1"), ep("ep2"), ep("ep3"), ep("ep4")}

	for {
		ok := true
		for _, p := range want {
			if !exists(p) {
				ok = false
				break
			}
		}

		if ok {
			return ep("ep1"), ep("ep2"), ep("ep3"), ep("ep4"), nil
		}
		if time.Now().After(deadline) {
			return "", "", "", "", fmt.Errorf("timeout waiting for %s", strings.Join(want, ", "))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
