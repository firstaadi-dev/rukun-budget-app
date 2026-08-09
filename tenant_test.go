package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Aplikasi ini multi-tenant, dan satu query yang lupa menyaring family_id
// berarti sebuah keluarga melihat isi rekening keluarga lain. Kesalahan itu
// tidak menimbulkan error, tidak membuat test lain merah, dan tidak terlihat
// sampai ada yang melapor — jadi dijaga di sini secara mekanis.
//
// Yang diperiksa: setiap query SQL di store.go yang menyentuh tabel data
// keluarga harus menyebut family_id. Bukan bukti kebenaran, tapi cukup untuk
// menangkap query baru yang ditambahkan tanpa penyaring.
func TestSetiapQueryDataMenyaringFamilyID(t *testing.T) {
	// Tabel yang isinya milik satu keluarga. sessions dan schema_migrations
	// tidak ada di sini: sesi terhubung ke keluarga lewat users, dan tabel
	// migrasi memang milik seluruh deployment.
	tabelData := []string{"wallets", "transactions", "categories", "investments"}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "store.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var pelanggaran []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		q := strings.ToLower(lit.Value)
		if !strings.Contains(q, "select") && !strings.Contains(q, "insert") &&
			!strings.Contains(q, "update") && !strings.Contains(q, "delete") {
			return true
		}
		// Query yang memang lintas keluarga: hanya yang membaca tabel families
		// itu sendiri, dipakai API admin.
		if strings.Contains(q, "from families") || strings.Contains(q, "into families") ||
			strings.Contains(q, "update families") {
			return true
		}
		for _, tabel := range tabelData {
			if !strings.Contains(q, tabel) {
				continue
			}
			if strings.Contains(q, "family_id") {
				continue
			}
			pos := fset.Position(lit.Pos())
			pelanggaran = append(pelanggaran,
				"store.go:"+itoa(pos.Line)+" menyentuh "+tabel+" tanpa menyaring family_id")
			break
		}
		return true
	})

	for _, v := range pelanggaran {
		t.Error(v)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Metode Store yang menyentuh data keluarga harus menerima familyID. Tanpa
// argumen itu, pemanggil tidak punya cara membatasi hasilnya, dan lupa
// membatasinya jadi mustahil terlihat di call site.
func TestMetodeStoreDataMenerimaFamilyID(t *testing.T) {
	// Metode yang memang tidak butuh: urusan sesi, akun, dan tabel families
	// yang dipakai API admin.
	dikecualikan := map[string]bool{
		"CreateSession": true, "SessionUser": true, "DeleteSession": true, "PurgeSessions": true,
		"FamilyByCode": true, "Families": true, "CreateFamily": true, "UpdateFamily": true,
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "store.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || !ast.IsExported(fn.Name.Name) || dikecualikan[fn.Name.Name] {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if ident, ok := star.X.(*ast.Ident); !ok || ident.Name != "Store" {
			continue
		}

		var punya bool
		for _, p := range fn.Type.Params.List {
			for _, name := range p.Names {
				if name.Name == "familyID" {
					punya = true
				}
			}
		}
		if !punya {
			t.Errorf("Store.%s menyentuh data keluarga tapi tidak menerima familyID; "+
				"tambahkan argumen itu atau daftarkan di pengecualian dengan alasannya",
				fn.Name.Name)
		}
	}
}
