-- Jatah bulanan dalam mata uang dasar aplikasi. Nol berarti belum disetel.
-- ponytail: mengganti BASE_CURRENCY perlu mengisi ulang jatah; simpan mata uang
-- per kategori bila pergantian mata uang dasar menjadi kebutuhan nyata.
ALTER TABLE categories ADD COLUMN budget_minor BIGINT NOT NULL DEFAULT 0 CHECK (budget_minor >= 0);
