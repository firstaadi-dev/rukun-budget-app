package main

import (
	"context"
	"errors"
	sqlcdb "github.com/firsta/rukun/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

// ---------- User & sesi ----------

type User struct {
	ID            int64
	Name          string
	Email         string
	FirebaseUID   string
	EmailVerified bool
	FamilyID      int64
	FamilyName    string
	// Kepala: anggota pertama keluarga ini, yaitu pembuat keluarga.
	// Dialah satu-satunya yang boleh menonaktifkan
	// anggota lain. Perannya diturunkan dari urutan pendaftaran, bukan disimpan
	// sebagai kolom sendiri: dengan begitu tidak ada keluarga yang bisa
	// kehilangan kepalanya karena satu baris data salah ubah.
	Kepala bool
	// Disabled: aksesnya sudah dicabut. Sesi yang sudah berjalan ikut mati
	// karena SessionUser menyaringnya.
	Disabled bool
}

// Member: satu anggota beserta jejaknya, untuk halaman pengaturan.
type Member struct {
	User
	CreatedAt time.Time
	// Txs: jumlah transaksi yang pernah dicatatnya. Ditampilkan supaya jelas
	// bahwa menonaktifkan anggota tidak menghapus apa pun yang sudah dicatat.
	Txs int
}

// kepalaKeluarga: potongan yang menandai anggota pertama sebuah keluarga.
const kepalaKeluarga = `u.id = (SELECT min(id) FROM users WHERE family_id = u.family_id)`

// Members mengurut sesuai urutan pendaftaran, jadi kepala keluarga selalu di
// atas dan anggota yang baru masuk selalu di bawah.
func (s *Store) Members(ctx context.Context, familyID int64) ([]Member, error) {
	rows, err := sqlcdb.New(s.db).GetMembers(ctx, pgtype.Int8{Int64: familyID, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]Member, len(rows))
	for i, row := range rows {
		out[i] = Member{User: User{ID: row.ID, Name: row.Name, FamilyID: familyID,
			Disabled: row.DisabledAt.Valid, Kepala: row.Kepala}, CreatedAt: row.CreatedAt.Time, Txs: int(row.TxCount)}
	}
	return out, nil
}

// SetMemberActive mencabut atau memulihkan akses seorang anggota. Kepala
// keluarga dikecualikan di query-nya sendiri, bukan hanya di handler: keluarga
// yang kepalanya nonaktif tidak punya siapa pun yang bisa memulihkannya lagi.
func (s *Store) SetMemberActive(ctx context.Context, familyID, id int64, aktif bool) error {
	var waktu pgtype.Timestamptz
	if !aktif {
		waktu = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	rows, err := sqlcdb.New(s.db).SetMemberActive(ctx, sqlcdb.SetMemberActiveParams{
		DisabledAt: waktu, ID: id, FamilyID: pgtype.Int8{Int64: familyID, Valid: true},
	})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteUserSessions memutus seluruh sesi seorang anggota di semua perangkat.
// Dipanggil setelah sandi diganti dan setelah akses dicabut — tanpa ini,
// keduanya baru benar-benar berlaku sebulan kemudian saat sesi lamanya habis.
func (s *Store) DeleteUserSessions(ctx context.Context, familyID, userID int64) error {
	return sqlcdb.New(s.db).DeleteUserSessions(ctx, sqlcdb.DeleteUserSessionsParams{
		ID: userID, FamilyID: pgtype.Int8{Int64: familyID, Valid: true},
	})
}

func (s *Store) CreateFirebaseUser(ctx context.Context, uid, email, name string) (int64, error) {
	return sqlcdb.New(s.db).CreateFirebaseUser(ctx, sqlcdb.CreateFirebaseUserParams{
		Name: name, Email: pgtype.Text{String: email, Valid: true},
		FirebaseUid: pgtype.Text{String: uid, Valid: true},
	})
}

func (s *Store) FirebaseUser(ctx context.Context, uid string) (User, error) {
	row, err := sqlcdb.New(s.db).FirebaseUser(ctx, pgtype.Text{String: uid, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return User{ID: row.ID, Name: row.Name, Email: row.Email.String, FirebaseUID: row.FirebaseUid.String,
		EmailVerified: row.EmailVerifiedAt.Valid, FamilyID: row.FamilyID, FamilyName: row.FamilyName,
		Kepala: row.Kepala, Disabled: row.DisabledAt.Valid}, nil
}

func (s *Store) MarkFirebaseEmailVerified(ctx context.Context, uid string) error {
	return sqlcdb.New(s.db).MarkFirebaseEmailVerified(ctx, pgtype.Text{String: uid, Valid: true})
}

func (s *Store) JoinFamily(ctx context.Context, userID int64, code, name string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := sqlcdb.New(tx)
	familyID, err := q.LockFamilyByCode(ctx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	rows, err := q.LinkUserToFamily(ctx, sqlcdb.LinkUserToFamilyParams{
		FamilyID: pgtype.Int8{Int64: familyID, Valid: true}, ID: userID, Name: name,
	})
	if err == nil && rows == 0 {
		return ErrAlreadyLinked
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateFamilyForUser(ctx context.Context, userID int64, name, code string) (Family, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Family{}, err
	}
	defer tx.Rollback(ctx)
	q := sqlcdb.New(tx)
	linked, err := q.UserLinkedForUpdate(ctx, userID)
	if err != nil {
		return Family{}, err
	}
	if linked {
		return Family{}, ErrAlreadyLinked
	}
	row, err := q.InsertFamily(ctx, sqlcdb.InsertFamilyParams{
		Name: name, SignupCode: code, BillingOwnerUserID: pgtype.Int8{Int64: userID, Valid: true},
	})
	if err != nil {
		return Family{}, err
	}
	f := Family{ID: row.ID, Name: row.Name, SignupCode: row.SignupCode,
		SignupCodeExpiresAt: row.SignupCodeExpiresAt.Time, CreatedAt: row.CreatedAt.Time}
	if err := q.AssignUserFamily(ctx, sqlcdb.AssignUserFamilyParams{
		FamilyID: pgtype.Int8{Int64: f.ID, Valid: true}, ID: userID,
	}); err != nil {
		return Family{}, err
	}
	if err := q.SeedFamilyCategories(ctx, f.ID); err != nil {
		return Family{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Family{}, err
	}
	return f, nil
}

func (s *Store) UserCount(ctx context.Context, familyID int64) (int, error) {
	n, err := sqlcdb.New(s.db).CountFamilyUsers(ctx, pgtype.Int8{Int64: familyID, Valid: true})
	return int(n), err
}

func (s *Store) CreateSession(ctx context.Context, token string, userID int64, until time.Time) error {
	return sqlcdb.New(s.db).CreateSession(ctx, sqlcdb.CreateSessionParams{
		Token: token, UserID: userID, ExpiresAt: pgtype.Timestamptz{Time: until, Valid: true},
	})
}

// SessionUser mengembalikan anggota beserta keluarganya. Dari sinilah familyID
// yang dipakai seluruh handler berasal — tidak pernah dari parameter URL atau
// isian form, yang bisa dikarang siapa saja.
//
// Anggota nonaktif disaring di sini, bukan di tiap handler. Setiap permintaan
// melewati satu query ini, jadi mencabut akses langsung berlaku di semua
// perangkatnya pada permintaan berikutnya — bukan sebulan lagi saat sesinya
// kedaluwarsa sendiri.
func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	row, err := sqlcdb.New(s.db).SessionUser(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return User{ID: row.ID, Name: row.Name, Email: row.Email.String, FirebaseUID: row.FirebaseUid.String,
		EmailVerified: row.EmailVerifiedAt.Valid, FamilyID: row.FamilyID, FamilyName: row.FamilyName,
		Kepala: row.Kepala}, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	return sqlcdb.New(s.db).DeleteSession(ctx, token)
}

func (s *Store) PurgeSessions(ctx context.Context) error {
	return sqlcdb.New(s.db).PurgeSessions(ctx)
}

// ---------- Keluarga ----------

type Family struct {
	ID                  int64
	Name                string
	SignupCode          string
	SignupCodeExpiresAt time.Time
	Members             int
	Wallets             int
	Txs                 int
	CreatedAt           time.Time
}

// FamilyByCode memvalidasi kode undangan saat akun bergabung.
func (s *Store) FamilyByCode(ctx context.Context, code string) (Family, error) {
	row, err := sqlcdb.New(s.db).GetFamilyByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return Family{}, ErrNotFound
	}
	return Family{ID: row.ID, Name: row.Name, SignupCode: row.SignupCode}, err
}

func (s *Store) FamilyByID(ctx context.Context, familyID int64) (Family, error) {
	row, err := sqlcdb.New(s.db).GetFamilyByID(ctx, familyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Family{}, ErrNotFound
	}
	return Family{ID: row.ID, Name: row.Name, SignupCode: row.SignupCode,
		SignupCodeExpiresAt: row.SignupCodeExpiresAt.Time}, err
}

func (s *Store) Families(ctx context.Context) ([]Family, error) {
	rows, err := sqlcdb.New(s.db).ListFamilies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Family, len(rows))
	for i, row := range rows {
		out[i] = Family{ID: row.ID, Name: row.Name, SignupCode: row.SignupCode,
			SignupCodeExpiresAt: row.SignupCodeExpiresAt.Time, CreatedAt: row.CreatedAt.Time,
			Members: int(row.Members), Wallets: int(row.Wallets), Txs: int(row.Txs)}
	}
	return out, nil
}

// UpdateFamily mengganti nama dan/atau kode undangan. Argumen kosong berarti
// tidak diubah, supaya pemanggil bisa memutar kode tanpa menyentuh namanya.
func (s *Store) UpdateFamily(ctx context.Context, id int64, name, code string) (Family, error) {
	q := sqlcdb.New(s.db)
	rows, err := q.UpdateFamily(ctx, sqlcdb.UpdateFamilyParams{Name: name, Code: code, ID: id})
	if err != nil {
		return Family{}, err
	}
	if rows == 0 {
		return Family{}, ErrNotFound
	}
	row, err := q.GetFamilyDetail(ctx, id)
	return Family{ID: row.ID, Name: row.Name, SignupCode: row.SignupCode,
		SignupCodeExpiresAt: row.SignupCodeExpiresAt.Time, CreatedAt: row.CreatedAt.Time}, err
}
