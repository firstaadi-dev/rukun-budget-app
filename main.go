package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	_ "time/tzdata" // Render menjalankan binary di image tanpa tzdata sistem.

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type App struct {
	store   *Store
	pages   map[string]*template.Template
	ver     string      // sidik jari aset statis, dipakai sebagai penanda versi di URL
	rateSrc *rateSource // kurs pasar dari API, boleh kosong kalau dimatikan
	loc     *time.Location
	base    string // mata uang dasar untuk total di dashboard
	admin   string // token API admin; kosong berarti API admin mati total
}

// parsePages menggabungkan layout dengan tiap halaman secara terpisah, supaya
// setiap halaman boleh mendefinisikan blok "content" bernama sama.
func parsePages() map[string]*template.Template {
	files, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		log.Fatal(err)
	}
	pages := map[string]*template.Template{}
	for _, f := range files {
		name := path.Base(f)
		if name == "layout.html" {
			continue
		}
		pages[name] = template.Must(template.New("layout.html").Funcs(tmplFuncs).
			ParseFS(templateFS, "templates/layout.html", f))
	}
	return pages
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.Ltime)

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL belum diisi")
	}
	loc, err := time.LoadLocation(env("APP_TZ", "Asia/Jakarta"))
	if err != nil {
		log.Fatalf("zona waktu tidak dikenal: %v", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("koneksi database: %v", err)
	}
	defer pool.Close()

	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := pool.Ping(startCtx); err != nil {
		log.Fatalf("database tidak merespons: %v", err)
	}
	if err := migrate(startCtx, pool); err != nil {
		log.Fatalf("gagal menyiapkan skema: %v", err)
	}

	app := &App{
		store: &Store{db: pool},
		pages: parsePages(),
		ver:   assetVersion(),
		loc:   loc,
		base:  env("BASE_CURRENCY", "IDR"),
		admin: os.Getenv("ADMIN_TOKEN"),
	}

	if app.admin == "" {
		log.Print("ADMIN_TOKEN kosong: API admin dimatikan, keluarga baru tidak bisa dibuat")
	}

	// "off" mematikan pengambilan kurs; aplikasi lalu hanya memakai kurs dari
	// transfer yang sudah tercatat.
	if u := env("RATES_URL", defaultRatesURL); u != "off" {
		app.rateSrc = newRateSource(u)
		go app.rateSrc.keep(ctx)
	} else {
		app.rateSrc = newRateSource("")
		log.Print("pengambilan kurs dimatikan (RATES_URL=off)")
	}

	go app.purgeSessionsDaily(ctx)

	addr := ":" + env("PORT", "8080")
	log.Printf("Rukun jalan di %s (zona %s, mata uang dasar %s)", addr, loc, app.base)
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func (a *App) purgeSessionsDaily(ctx context.Context) {
	for {
		if err := a.store.PurgeSessions(ctx); err != nil {
			log.Printf("purge sesi: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", staticHandler())
	// Service worker harus disajikan dari root: cakupannya mengikuti path file
	// ini, dan dari /static/ ia tidak akan mengendalikan halaman aplikasi.
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, staticFS, "static/sw.js")
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /masuk", a.loginForm)
	mux.HandleFunc("POST /masuk", a.login)
	mux.HandleFunc("GET /daftar", a.registerForm)
	mux.HandleFunc("POST /daftar", a.register)
	mux.HandleFunc("POST /keluar", a.logout)

	// API admin sengaja tidak punya UI: pembuatan keluarga dilakukan developer
	// lewat curl atau Postman, bukan oleh siapa pun yang membuka aplikasi.
	mux.Handle("GET /admin/keluarga", a.requireAdmin(a.adminListFamilies))
	mux.Handle("POST /admin/keluarga", a.requireAdmin(a.adminCreateFamily))
	mux.Handle("PATCH /admin/keluarga/{id}", a.requireAdmin(a.adminPatchFamily))

	auth := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, a.requireUser(h)) }

	auth("GET /{$}", a.dashboard)

	auth("GET /dompet", a.walletList)
	auth("GET /dompet/baru", a.walletForm)
	auth("POST /dompet/baru", a.walletCreate)
	auth("GET /dompet/{id}/ubah", a.walletForm)
	auth("POST /dompet/{id}/ubah", a.walletUpdate)
	auth("POST /dompet/{id}/hapus", a.walletDelete)

	auth("GET /kategori", a.categoryList)
	auth("GET /kategori/baru", a.categoryForm)
	auth("POST /kategori/baru", a.categoryCreate)
	auth("GET /kategori/{id}/ubah", a.categoryForm)
	auth("POST /kategori/{id}/ubah", a.categoryUpdate)
	auth("POST /kategori/{id}/hapus", a.categoryDelete)

	auth("GET /transaksi", a.txList)
	auth("GET /transaksi/baru", a.txForm)
	auth("POST /transaksi/baru", a.txCreate)
	auth("GET /transaksi/{id}", a.txDetail)
	auth("GET /transaksi/{id}/ubah", a.txForm)
	auth("POST /transaksi/{id}/ubah", a.txUpdate)
	auth("POST /transaksi/{id}/hapus", a.txDelete)

	return mux
}

// assetVersion meringkas seluruh isi aset statis jadi satu penanda pendek.
// Penanda ini ditempel sebagai ?v= di URL aset, sehingga setiap deploy yang
// mengubah CSS atau JS otomatis memakai URL baru. Tanpa ini, browser yang
// sudah menyimpan versi lama akan memakainya sampai cache-nya kedaluwarsa —
// user melihat tampilan lama dan tidak ada cara menyuruhnya menyegarkan.
func assetVersion() string {
	h := sha256.New()
	err := fs.WalkDir(staticFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	if err != nil {
		log.Fatalf("membaca aset statis: %v", err)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// staticHandler menyajikan aset bawaan binary. URL yang membawa ?v= boleh
// di-cache selamanya karena penandanya berubah begitu isinya berubah; URL
// tanpa penanda hanya di-cache sebentar.
func staticHandler() http.Handler {
	fileServer := http.FileServerFS(staticFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
		}
		fileServer.ServeHTTP(w, r)
	})
}

var tmplFuncs = template.FuncMap{
	"symbol": Symbol,
	"lower":  strings.ToLower,
	// dict merakit map untuk mengoper beberapa nilai ke satu blok template.
	"dict": func(kv ...any) map[string]any {
		m := make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			key, _ := kv[i].(string)
			m[key] = kv[i+1]
		}
		return m
	},
	"iso":   func(t time.Time) string { return t.Format("2006-01-02") },
	"waktu": func(t time.Time) string { return t.Format("15:04") },
}
