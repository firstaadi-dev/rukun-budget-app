package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Aplikasi ini multi-tenant, dan satu query yang lupa menyaring family_id
// berarti sebuah keluarga melihat isi rekening keluarga lain. Kesalahan itu
// tidak menimbulkan error, tidak membuat test lain merah, dan tidak terlihat
// sampai ada yang melapor — jadi dijaga di sini secara mekanis.
//
// Yang diperiksa: setiap query SQL di file store dan sqlc yang menyentuh tabel data
// keluarga harus menyebut family_id. Bukan bukti kebenaran, tapi cukup untuk
// menangkap query baru yang ditambahkan tanpa penyaring.
func TestSetiapQueryDataMenyaringFamilyID(t *testing.T) {
	// Tabel yang isinya milik satu keluarga. sessions dan schema_migrations
	// tidak ada di sini: sesi terhubung ke keluarga lewat users, dan tabel
	// migrasi memang milik seluruh deployment.
	tabelData := []string{"wallets", "transactions", "categories", "investments"}

	fset := token.NewFileSet()
	files, err := filepath.Glob("store*.go")
	if err != nil {
		t.Fatal(err)
	}

	var pelanggaran []string
	check := func(q, location string) {
		q = strings.ToLower(q)
		if !strings.Contains(q, "select") && !strings.Contains(q, "insert") &&
			!strings.Contains(q, "update") && !strings.Contains(q, "delete") {
			return
		}
		// Query yang memang lintas keluarga: yang membaca tabel families itu
		// sendiri, dipakai API admin.
		if strings.Contains(q, "from families") || strings.Contains(q, "into families") ||
			strings.Contains(q, "update families") {
			return
		}
		// Daftar simbol yang perlu diambil harganya. Ia menyentuh investments,
		// tapi yang dibacanya cuma kode bursa dan jenis posisinya — bukan berapa
		// banyak, bukan berapa modalnya, bukan milik siapa. Penyegar harian yang
		// memanggilnya tidak mewakili satu keluarga pun, dan meminta harga yang
		// sama sekali per keluarga justru memperbesar kemungkinan ditolak
		// sumbernya. Lihat SimbolDipakai.
		if strings.Contains(q, "distinct symbol, kind from investments") {
			return
		}
		if strings.Contains(q, "nextval('tx_series_seq')") {
			return // sequence global; tidak membaca transaksi keluarga
		}
		for _, tabel := range tabelData {
			if !strings.Contains(q, tabel) {
				continue
			}
			if strings.Contains(q, "family_id") {
				continue
			}
			pelanggaran = append(pelanggaran, location+" menyentuh "+tabel+" tanpa menyaring family_id")
			break
		}
	}
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				check(lit.Value, fset.Position(lit.Pos()).String())
			}
			return true
		})
	}
	queries, err := filepath.Glob("sqlc/queries/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range queries {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, query := range strings.Split(string(body), "-- name:") {
			check(query, name)
		}
	}

	for _, v := range pelanggaran {
		t.Error(v)
	}
}

// Metode Store yang menyentuh data keluarga harus menerima familyID. Tanpa
// argumen itu, pemanggil tidak punya cara membatasi hasilnya, dan lupa
// membatasinya jadi mustahil terlihat di call site.
func TestMetodeStoreDataMenerimaFamilyID(t *testing.T) {
	// Metode yang memang tidak butuh: urusan sesi, akun, dan tabel families
	// yang dipakai API admin.
	dikecualikan := map[string]bool{
		"CreateSession": true, "SessionUser": true, "DeleteSession": true, "PurgeSessions": true,
		"FamilyByCode": true, "Families": true, "UpdateFamily": true,
		// Firebase UID/email adalah identitas akun global, lalu dipetakan ke
		// satu user Rukun. Tidak membaca catatan keuangan keluarga.
		"CreateFirebaseUser": true, "FirebaseUser": true, "MarkFirebaseEmailVerified": true,
		// Akun mandiri belum punya familyID. Pengaitan keluarga hanya boleh
		// melalui CreateFamilyForUser/JoinFamily saat family_id masih NULL.
		"JoinFamily": true, "CreateFamilyForUser": true,

		// Harga pasar, bukan catatan keluarga. Harga SPUS sama untuk siapa pun
		// yang memegangnya, tidak ada satu pun angka milik keluarga di tabel
		// quotes, dan menyalinnya per keluarga cuma melipatgandakan permintaan
		// ke sumber yang memang sedang membatasi kami. Lihat migrasi 009.
		"Quotes": true, "SaveQuotes": true, "AllQuotes": true,
		// Membaca investments, tapi hanya kode bursa dan jenis posisinya —
		// alasan lengkapnya di TestSetiapQueryDataMenyaringFamilyID di atas.
		"SimbolDipakai": true,
	}

	fset := token.NewFileSet()
	files, err := filepath.Glob("store*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
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
}
