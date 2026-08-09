package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Mencabut akses seorang anggota hanya berarti kalau sesinya ikut berhenti
// berlaku. Penyaringnya cuma ada di satu tempat — SessionUser, yang dilewati
// setiap permintaan — dan kalau penyaring itu hilang, tidak ada test lain yang
// merah dan tidak ada error yang muncul: anggota yang sudah dicabut tetap bisa
// membuka seluruh isi keuangan keluarga sampai sesinya kedaluwarsa sendiri.
// Karena itu keberadaannya dijaga di sini secara mekanis.
func TestSessionUserMenyaringAnggotaNonaktif(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "store.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var ketemu, menyaring bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "SessionUser" {
			continue
		}
		ketemu = true
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING &&
				strings.Contains(strings.ToLower(lit.Value), "disabled_at is null") {
				menyaring = true
			}
			return true
		})
	}

	if !ketemu {
		t.Fatal("Store.SessionUser tidak ditemukan di store.go")
	}
	if !menyaring {
		t.Error("Store.SessionUser tidak menyaring disabled_at IS NULL; " +
			"anggota yang aksesnya dicabut akan tetap bisa memakai sesi lamanya")
	}
}
