// Rukun — skrip pelengkap form. Semua halaman tetap berfungsi tanpa file ini;
// yang hilang cuma isian otomatis, bukan kemampuan menyimpan data.

const EXP = { JPY: 0, KRW: 0, VND: 0, CLP: 0, ISK: 0, BHD: 3, KWD: 3, JOD: 3, OMR: 3, TND: 3 };
const exp = (cur) => (cur in EXP ? EXP[cur] : 2);

// parseNum mengikuti aturan ParseAmount di money.go: pemisah desimal adalah
// titik/koma terakhir yang diikuti 1-2 digit, sisanya pemisah ribuan.
function parseNum(s) {
  s = String(s || '').replace(/\s/g, '').replace(/^[^\d,.-]+/, '');
  if (!s) return null;
  const neg = s.startsWith('-');
  s = s.replace(/^[+-]/, '');
  let whole = s, frac = '';
  const i = Math.max(s.lastIndexOf('.'), s.lastIndexOf(','));
  if (i >= 0) {
    const d = s.length - i - 1;
    if (d >= 1 && d <= 2) { whole = s.slice(0, i); frac = s.slice(i + 1); }
  }
  whole = whole.replace(/[.,]/g, '');
  if (!/^\d*$/.test(whole) || !/^\d*$/.test(frac)) return null;
  if (whole === '' && frac === '') return null;
  const v = Number((whole || '0') + '.' + (frac || '0'));
  return Number.isFinite(v) ? (neg ? -v : v) : null;
}

// fmtPlain mencerminkan FormatPlain di money.go.
function fmtPlain(v, cur) {
  if (v === null || !Number.isFinite(v)) return '';
  const e = exp(cur);
  const neg = v < 0;
  const fixed = Math.abs(v).toFixed(e);
  const [w, f] = fixed.split('.');
  let out = w.replace(/\B(?=(\d{3})+(?!\d))/g, '.');
  if (e > 0 && !(cur === 'IDR' && Number(f) === 0)) out += ',' + f;
  return (neg ? '-' : '') + out;
}

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
const curOf = (sel) => sel.selectedOptions[0]?.dataset.currency || 'IDR';
const symOf = (sel) => sel.selectedOptions[0]?.dataset.symbol || '';

// ---------- form dompet: daftar penyedia mengikuti jenis ----------

(function walletForm() {
  const form = $('#wallet-form');
  if (!form) return;

  const providers = JSON.parse($('#provider-data')?.dataset.providers || '{}');
  const list = $('#provider-list', form);
  const field = $('#provider-field', form);
  const currency = $('#mata_uang', form);

  function sync() {
    const jenis = $$('[data-jenis]', form).find((r) => r.checked)?.value || 'bank';
    const opts = providers[jenis] || [];
    field.hidden = opts.length === 0; // Tunai tidak punya penyedia
    list.innerHTML = opts.map((p) => `<option value="${p}">`).join('');
  }
  function syncSymbol() {
    $$('[data-currency-symbol]', form).forEach((el) => {
      el.textContent = currency.selectedOptions[0].value === 'IDR' ? 'Rp' : currency.value;
    });
  }

  $$('[data-jenis]', form).forEach((r) => r.addEventListener('change', sync));
  currency.addEventListener('change', syncSymbol);
  sync();
})();

// ---------- form pengeluaran / pemasukan: prefix ikut mata uang dompet ----------

(function amountForm() {
  const form = $('[data-amount-form]');
  if (!form) return;
  const wallet = $('[data-wallet-select]', form);

  function sync() {
    $$('[data-currency-symbol]', form).forEach((el) => (el.textContent = symOf(wallet)));
    $$('[data-currency-code]', form).forEach((el) => (el.textContent = curOf(wallet)));
  }
  wallet.addEventListener('change', sync);
  sync();
})();

// ---------- form transfer ----------

(function transferForm() {
  const form = $('#transfer-form');
  if (!form) return;

  const raw = JSON.parse(form.dataset.rates || '{}');
  const base = form.dataset.base || 'IDR';
  const from = $('[data-from]', form);
  const to = $('[data-to]', form);
  const outEl = $('#nominal_keluar', form);
  const rateEl = $('#kurs', form);
  const inEl = $('#nominal_diterima', form);
  const feeEl = $('#biaya_admin', form);
  const rateField = $('[data-rate-field]', form);
  const crossEl = $('[data-cross]', form);
  const customTag = $('[data-rate-custom]', form);

  // "fromMinor:toMinor:powFrom:powTo" -> nilai major per 1 unit sumber
  function known(a, b) {
    const s = raw[a + '>' + b];
    if (!s) return null;
    const [fm, tm, pf, pt] = s.split(':').map(Number);
    if (!fm || !tm) return null;
    return { fromMajor: fm / pf, toMajor: tm / pt };
  }

  // Arah kutipan kurs harus sama persis dengan quoteDirection di handlers.go,
  // kalau tidak angka yang diketik user akan ditafsirkan terbalik oleh server.
  function quoteDir(a, b) {
    const r = known(a, b);
    if (r) return r.fromMajor >= r.toMajor ? [a, b] : [b, a];
    if (b === base) return [base, a];
    return [a, b];
  }

  function defaultFactor(a, b) {
    const r = known(a, b);
    return r ? r.toMajor / r.fromMajor : null;
  }

  let priceCur = '', perCur = '', defFactor = null;

  // faktor konversi sumber -> tujuan, dari angka kurs yang tampil
  function factor() {
    const v = parseNum(rateEl.value);
    if (v === null || v <= 0) return defFactor;
    const a = curOf(from), b = curOf(to);
    if (perCur === a && priceCur === b) return v;
    if (perCur === b && priceCur === a) return 1 / v;
    return defFactor;
  }

  function setRateFromFactor(f) {
    if (!f) { rateEl.value = ''; return; }
    const a = curOf(from);
    const price = perCur === a ? f : 1 / f;
    rateEl.value = fmtPlain(price, priceCur);
  }

  function syncPair() {
    const a = curOf(from), b = curOf(to);
    const cross = a !== b;

    $$('[data-sym-from]', form).forEach((el) => (el.textContent = symOf(from)));
    $$('[data-sym-to]', form).forEach((el) => (el.textContent = symOf(to)));

    rateField.hidden = !cross;
    crossEl.hidden = !cross;
    if (!cross) {
      rateEl.value = '';
      defFactor = null;
      if (customTag) customTag.hidden = true;
      return;
    }

    crossEl.textContent = `Transfer lintas mata uang: ${a} → ${b}`;
    [priceCur, perCur] = quoteDir(a, b);
    defFactor = defaultFactor(a, b);

    $$('[data-sym-price]', form).forEach((el) => {
      el.textContent = priceCur === 'IDR' ? 'Rp' : priceCur;
    });
    $$('[data-rate-per]', form).forEach((el) => (el.textContent = `1 ${perCur} =`));

    const hint = $('[data-rate-default]', form);
    if (defFactor) {
      const price = perCur === a ? defFactor : 1 / defFactor;
      hint.textContent = `· kurs terakhir: ${fmtPlain(price, priceCur)}`;
      if (!rateEl.value) setRateFromFactor(defFactor);
    } else {
      hint.textContent = '· belum ada catatan kurs, isi manual';
    }
    markCustom();
  }

  function markCustom() {
    if (!customTag) return;
    const f = factor();
    customTag.hidden = !defFactor || !f || Math.abs(f - defFactor) < defFactor * 1e-9;
  }

  const outCur = () => curOf(from);
  const inCur = () => curOf(to);

  // Biaya admin adalah variabel penyeimbang: ia yang menyerap selisih, kecuali
  // saat user memang sedang mengetik di kolom biaya admin.
  function recalcFromFee() {
    const out = parseNum(outEl.value), fee = parseNum(feeEl.value) || 0, f = factor();
    if (out === null || !f) return;
    inEl.value = fmtPlain(Math.max(0, (out - fee) * f), inCur());
  }
  function recalcFee() {
    const out = parseNum(outEl.value), got = parseNum(inEl.value), f = factor();
    if (out === null || got === null || !f) return;
    feeEl.value = fmtPlain(out - got / f, outCur());
  }
  function sameCurrencyFee() {
    const out = parseNum(outEl.value), got = parseNum(inEl.value);
    if (out === null || got === null) return;
    feeEl.value = fmtPlain(out - got, outCur());
  }

  function onOut() {
    if (outCur() === inCur()) {
      if (!inEl.value) { inEl.value = outEl.value; feeEl.value = ''; return; }
      sameCurrencyFee();
      return;
    }
    if (!inEl.value) recalcFromFee();
    else recalcFee();
  }
  function onIn() {
    if (outCur() === inCur()) sameCurrencyFee();
    else recalcFee();
  }

  // Ganti dompet berarti mata uangnya bisa berubah. Nominal diterima dan biaya
  // admin yang lama sudah tidak punya arti di mata uang baru, jadi dikosongkan
  // dulu — kalau tidak, biaya admin akan terhitung dari dua mata uang berbeda.
  function onPairChange() {
    inEl.value = '';
    feeEl.value = '';
    syncPair();
    onOut();
  }
  from.addEventListener('change', onPairChange);
  to.addEventListener('change', onPairChange);
  outEl.addEventListener('input', onOut);
  inEl.addEventListener('input', onIn);
  feeEl.addEventListener('input', recalcFromFee);
  rateEl.addEventListener('input', () => { markCustom(); recalcFromFee(); });

  syncPair();
})();

// ---------- PWA ----------

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => navigator.serviceWorker.register('/sw.js'));
}
