package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveExportRejectsABrokenZip(t *testing.T) {
	var good bytes.Buffer
	z := zip.NewWriter(&good)
	f, _ := z.Create("README.txt")
	f.Write([]byte("hi"))
	z.Close()

	dir := t.TempDir()
	ok := filepath.Join(dir, "ok.zip")
	if _, err := saveExport(bytes.NewReader(good.Bytes()), ok); err != nil {
		t.Fatalf("good zip: %v", err)
	}
	bad := filepath.Join(dir, "bad.zip")
	if _, err := saveExport(bytes.NewReader(good.Bytes()[:good.Len()-10]), bad); err == nil {
		t.Fatal("a zip without its end record was accepted")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("the broken file was left behind")
	}
}
