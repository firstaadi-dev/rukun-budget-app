// Rukun — isian otomatis, perhitungan transfer, dan kontrol form.

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

// floorTo membulatkan ke bawah pada satuan terkecil mata uang. Nominal diterima
// tidak boleh dibulatkan ke atas: uang yang keluar tidak cukup membeli sen
// tambahan itu, dan server akan menolaknya sebagai "diterima melebihi keluar".
// Epsilon-nya menutup galat biner, bukan kelebihan yang sungguhan.
function floorTo(v, cur) {
  const p = Math.pow(10, exp(cur));
  return Math.floor(v * p + 1e-6) / p;
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

// ---------- tema tampilan ----------

const themeChoices = $$('[data-theme-choice]');
function setTheme(theme, save = false) {
  const value = theme === 'ceria' ? 'ceria' : 'klasik';
  document.documentElement.dataset.theme = value;
  themeChoices.forEach((button) => button.setAttribute('aria-pressed', String(button.dataset.themeChoice === value)));
  const meta = $('meta[name="theme-color"]');
  if (meta) meta.content = '#f9f9f6';
  if (save) {
    try { localStorage.setItem('rukun-theme', value); } catch { /* Tema tetap berlaku sampai halaman dibuka ulang. */ }
  }
}
themeChoices.forEach((button) => button.addEventListener('click', () => setTheme(button.dataset.themeChoice, true)));
try { setTheme(localStorage.getItem('rukun-theme') || 'ceria'); } catch { setTheme('ceria'); }

// Diagram laporan tetap punya daftar kategori berupa tautan sebagai fallback;
// ECharts menambah tooltip dan navigasi langsung dari irisan diagram.
if (window.echarts) {
  const chartColors = ['#0d6e6e', '#006c49', '#b43438', '#4f86ac', '#98729d', '#718c52', '#cc8847', '#4f8b91'];
  $$('[data-report-chart]').forEach((el) => {
    const rows = $$('[data-chart-category]', el.parentElement);
    const chart = window.echarts.init(el);
    chart.setOption({
      aria: { enabled: true },
      tooltip: { trigger: 'item', renderMode: 'richText', formatter: ({ name, percent }) => `${name}: ${percent}%` },
      series: [{
        type: 'pie',
        radius: ['42%', '76%'],
        selectedMode: 'single',
        data: rows.map((row) => ({
          name: row.dataset.chartCategory,
          value: Number(row.dataset.chartShare),
          itemStyle: { color: chartColors[Number(row.dataset.chartColor) % chartColors.length] },
        })),
        label: { show: false },
        emphasis: { scale: true, scaleSize: 6 },
      }],
    });
    chart.on('click', ({ name }) => {
      const target = new URL('/transaksi', window.location.href);
      target.searchParams.set('periode', el.dataset.period);
      target.searchParams.set('kategori', name);
      window.location.assign(target);
    });
    new ResizeObserver(() => chart.resize()).observe(el);
  });
}

// ---------- kolom nominal: hanya angka, dengan pemisah ribuan ----------

// inputmode="decimal" cuma memberi saran keyboard di ponsel; di desktop huruf
// tetap bisa diketik dan salahnya baru ketahuan setelah menekan Simpan.
// Atribut pattern menjaga saat skrip mati, penyaring ini menjaga saat hidup —
// sekaligus menyisipkan pemisah ribuan supaya nominal besar bisa dibaca sambil
// diketik, bukan cuma setelah disimpan.

function bersihkanAngka(s) {
  const negatif = s.startsWith('-'); // saldo awal kartu kredit boleh minus
  return (negatif ? '-' : '') + s.replace(/[^\d.,]/g, '');
}

// pisahRibuan memformat ulang isian mengikuti konvensi Indonesia: koma milik
// user sebagai pemisah desimal, titik disisipkan sendiri sebagai pemisah
// ribuan. Titik yang diketik user diserap, bukan diartikan desimal — kalau
// tidak, mengetik "1." pada "1.234" akan berubah jadi "1," di tengah jalan.
//
// maks membatasi angka di belakang koma. Dua untuk nominal uang, tapi kuantitas
// menyimpan delapan dan harga satuan empat lebih halus dari satuan terkecil
// mata uangnya — memotongnya di dua akan memangkas "1.234,5678" jadi
// "1.234,56" sambil diketik, dan yang tersimpan bukan yang dimaksud.
function pisahRibuan(s, maks = 2) {
  const negatif = s.startsWith('-');
  const isi = negatif ? s.slice(1) : s;

  let bulat = isi, desimal = null;
  const koma = isi.indexOf(',');
  if (koma >= 0) {
    bulat = isi.slice(0, koma);
    desimal = isi.slice(koma + 1).replace(/[.,]/g, '').slice(0, maks);
  }
  bulat = bulat.replace(/[.,]/g, '').replace(/\B(?=(\d{3})+(?!\d))/g, '.');

  return (negatif ? '-' : '') + bulat + (desimal === null ? '' : ',' + desimal);
}

// Kursor dijaga lewat jumlah karakter bermakna sebelumnya — digit, koma, dan
// tanda minus — bukan lewat indeks mentah. Pemisah ribuan muncul dan hilang
// sendiri saat angkanya tumbuh, jadi indeks mentah akan meleset.
const PENTING = /[\d,-]/;

function hitungPenting(s) {
  let n = 0;
  for (const ch of s) if (PENTING.test(ch)) n++;
  return n;
}

function posisiSetelah(s, jumlah) {
  if (jumlah <= 0) return 0;
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    if (PENTING.test(s[i])) n++;
    if (n >= jumlah) return i + 1;
  }
  return s.length;
}

$$('input[inputmode="decimal"]').forEach((el) => {
  // Menghapus mundur tepat di belakang pemisah ribuan seharusnya menghapus
  // angkanya, bukan pemisahnya: pemisah itu kita yang menaruh, dan
  // menghilangkannya sendirian membuat tombol backspace terasa tidak berfungsi.
  el.addEventListener('beforeinput', (e) => {
    if (e.inputType !== 'deleteContentBackward') return;
    const p = el.selectionStart;
    if (p === null || p !== el.selectionEnd || p < 2 || el.value[p - 1] !== '.') return;
    e.preventDefault();
    el.value = el.value.slice(0, p - 2) + el.value.slice(p);
    el.setSelectionRange(p - 2, p - 2);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  });

  el.addEventListener('input', () => {
    const asli = el.value;
    const hasil = pisahRibuan(bersihkanAngka(asli), Number(el.dataset.desimal) || 2);
    if (hasil === asli) return;
    const pos = el.selectionStart ?? asli.length;
    const penting = hitungPenting(asli.slice(0, pos));
    el.value = hasil;
    el.setSelectionRange(posisiSetelah(hasil, penting), posisiSetelah(hasil, penting));
  });
});

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

  // Limit dan siklus tagihan hanya berarti untuk akun berbasis kredit.
  function syncSiklus() {
    const jenis = $$('[data-jenis]', form).find((r) => r.checked)?.value;
    const kredit = jenis === 'credit' || jenis === 'paylater';
    for (const id of ['#siklus-kartu', '#limit-kredit']) {
      const box = $(id, form);
      if (box) box.hidden = !kredit;
    }
  }

  $$('[data-jenis]', form).forEach((r) => {
    r.addEventListener('change', sync);
    r.addEventListener('change', syncSiklus);
  });
  currency.addEventListener('change', syncSymbol);
  sync();
  syncSiklus();
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

// ---------- form hutang piutang ----------

// Mata uang hanya dipilih sendiri saat tidak ada dompet. Begitu dompet dipilih,
// mata uangnya mengikuti dompet itu — dua sumber kebenaran untuk hal yang sama
// adalah cara paling mudah membuat nominal tersimpan dengan satuan yang salah.
(function hutangForm() {
  const form = $('#hutang-form');
  if (!form) return;
  const wallet = $('[data-wallet-select]', form);
  const curField = $('[data-currency-field]', form);
  const curSelect = $('#mata_uang', form);

  function sync() {
    const pakaiDompet = wallet.value !== '';
    curField.hidden = pakaiDompet;
    const cur = pakaiDompet ? curOf(wallet) : curSelect.value;
    const sym = pakaiDompet ? symOf(wallet) : (cur === 'IDR' ? 'Rp' : cur);
    $$('[data-currency-symbol]', form).forEach((el) => (el.textContent = sym));
  }
  wallet.addEventListener('change', sync);
  curSelect.addEventListener('change', sync);
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
  const hintEl = $('[data-rate-default]', form);

  // Kolom mana yang diturunkan dari kolom lain. Defaultnya nominal diterima:
  // user mengetik nominal keluar, kurs mengisi sisanya, biaya admin nol.
  // Begitu user mengetik sendiri di nominal diterima, dialah yang dipegang dan
  // biaya admin yang menyesuaikan — itu memang arti selisihnya.
  let derive = 'in';

  // "fromMinor:toMinor:powFrom:powTo:sumber"
  function known(a, b) {
    const s = raw[a + '>' + b];
    if (!s) return null;
    const [fm, tm, pf, pt, src] = s.split(':');
    if (!Number(fm) || !Number(tm)) return null;
    return { fromMajor: Number(fm) / Number(pf), toMajor: Number(tm) / Number(pt), src };
  }

  // Arah kutipan kurs harus sama persis dengan quoteDirection di handler_transaction.go,
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

  const outCur = () => curOf(from);
  const inCur = () => curOf(to);

  // Faktor konversi sumber -> tujuan. Sesama mata uang selalu 1, sehingga
  // seluruh perhitungan di bawah tidak perlu cabang khusus.
  function factor() {
    if (outCur() === inCur()) return 1;
    const v = parseNum(rateEl.value);
    if (v === null || v <= 0) return defFactor;
    if (perCur === outCur() && priceCur === inCur()) return v;
    if (perCur === inCur() && priceCur === outCur()) return 1 / v;
    return defFactor;
  }

  function setRateFromFactor(f) {
    if (!f) { rateEl.value = ''; return; }
    rateEl.value = fmtPlain(perCur === outCur() ? f : 1 / f, priceCur);
  }

  // Satu-satunya tempat yang menulis angka: arah penurunannya ditentukan
  // `derive`, jadi dua kolom tidak pernah saling menimpa.
  function recalc() {
    const out = parseNum(outEl.value);
    const f = factor();
    if (out === null || !f) return;

    if (derive === 'in') {
      const fee = parseNum(feeEl.value) || 0;
      inEl.value = fmtPlain(floorTo(Math.max(0, (out - fee) * f), inCur()), inCur());
      return;
    }
    const got = parseNum(inEl.value);
    if (got === null) return;
    feeEl.value = fmtPlain(out - got / f, outCur());
  }

  function syncPair() {
    const a = outCur(), b = inCur();
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

    crossEl.textContent = `Transfer lintas mata uang: ${a} \u2192 ${b}`;
    [priceCur, perCur] = quoteDir(a, b);
    defFactor = defaultFactor(a, b);

    $$('[data-sym-price]', form).forEach((el) => {
      el.textContent = priceCur === 'IDR' ? 'Rp' : priceCur;
    });
    $$('[data-rate-per]', form).forEach((el) => (el.textContent = `1 ${perCur} =`));

    if (defFactor) {
      const price = fmtPlain(perCur === a ? defFactor : 1 / defFactor, priceCur);
      const info = known(a, b);
      const asal = info && info.src === 'pasar'
        ? `kurs pasar${form.dataset.rateUpdated ? ' ' + form.dataset.rateUpdated : ''}`
        : 'kurs transfer terakhir';
      hintEl.textContent = `\u00b7 ${asal}: ${price}`;
      if (!rateEl.value) setRateFromFactor(defFactor);
    } else {
      hintEl.textContent = '\u00b7 belum ada kurs acuan, isi manual';
    }
    markCustom();
    if (customToggle) {
      const current = factor();
      customToggle.checked = !defFactor || (current && Math.abs(current - defFactor) >= defFactor * 1e-9);
      rateEl.readOnly = !customToggle.checked;
    }
  }

  function markCustom() {
    if (!customTag) return;
    const f = factor();
    customTag.hidden = !defFactor || !f || Math.abs(f - defFactor) < defFactor * 1e-9;
  }

  // Ganti dompet berarti mata uangnya bisa berubah. Nominal diterima dan biaya
  // admin yang lama sudah tidak punya arti di mata uang baru, jadi dikosongkan
  // dan penurunan dikembalikan ke keadaan awal.
  function onPairChange() {
    derive = 'in';
    inEl.value = '';
    feeEl.value = '';
    rateEl.value = '';
    syncPair();
    recalc();
  }

  from.addEventListener('change', onPairChange);
  to.addEventListener('change', onPairChange);
  outEl.addEventListener('input', recalc);
  rateEl.addEventListener('input', () => { markCustom(); recalc(); });
  inEl.addEventListener('input', () => { derive = 'fee'; recalc(); });
  feeEl.addEventListener('input', () => { derive = 'in'; recalc(); });

  // Saat mengubah transfer lama, nominal diterima sudah tersimpan apa adanya:
  // jangan dihitung ulang, biarkan biaya admin yang menyesuaikan.
  if (inEl.value) derive = 'fee';
  const customToggle = $('[data-custom-rate-toggle]', form);
  const resetRate = () => {setRateFromFactor(defFactor);derive='in';markCustom();recalc();form.dispatchEvent(new Event('change', {bubbles:true}));};
  $('[data-rate-reset]', form)?.addEventListener('click', resetRate);
  customToggle?.addEventListener('change', () => {rateEl.readOnly=!customToggle.checked;if(!customToggle.checked)resetRate();else rateEl.focus();});
  syncPair();
})();

// ---------- filter yang mengirim sendiri saat pilihannya berubah ----------

$$('[data-auto-submit] select').forEach((sel) => {
  sel.addEventListener('change', () => sel.form.submit());
});

$$('[data-search-debounce] input[type="search"]').forEach((input) => {
  let timer;
  input.addEventListener('input', () => {
    clearTimeout(timer);
    timer = setTimeout(() => input.form.requestSubmit(), 500);
  });
});

// ---------- period picker: draft range, applied only on submit ----------
$$('[data-period-picker]').forEach(form => {
 const dialog = form.closest('dialog');
 const from = form.elements.dari, to = form.elements.sampai, period = form.elements.periode;
 const grid = $('[data-period-grid]', form);
 const parse = value => /^\d{4}-\d{2}-\d{2}$/.test(value || '') && !value.startsWith('0001') ? new Date(`${value}T00:00:00`) : null;
 const iso = date => `${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`;
 const dayNumber = date => Date.UTC(date.getFullYear(),date.getMonth(),date.getDate()) / 86400000;
 const daysBetween = (start,end) => dayNumber(end)-dayNumber(start)+1;
 const today = parse(form.dataset.today) || new Date();
 const original = {from:from.value,to:to.value,period:period.value,mode:form.dataset.initialMode};
 let mode=original.mode, activePreset='', editingEnd=false;
 let cursor = parse(from.value) || today;
 cursor = new Date(cursor.getFullYear(),cursor.getMonth(),1);
 const dateLabel = date => date ? date.toLocaleDateString('id-ID',{day:'numeric',month:'short',year:'numeric'}) : 'Pilih tanggal';
 const presets = $$('[data-period-preset]',form);
 function render() {
  from.disabled=to.disabled=false;
  period.disabled=mode==='custom';
  from.required=to.required=mode==='custom';
  const start=parse(from.value),end=parse(to.value);
  const count=start&&end?daysBetween(start,end):0;
  $('[data-period-from-label]',form).textContent=dateLabel(start);
  $('[data-period-to-label]',form).textContent=dateLabel(end);
  $('[data-period-days]',form).textContent=count>0?`${count} Hari`:'— Hari';
  $('[data-period-apply-days]',form).textContent=count>0?`(${count} Hari)`:'';
  $('[data-period-start-badge]',form).textContent=mode==='cycle'?'Mulai Siklus':'Tanggal Mulai';
  $('[data-period-end-badge]',form).textContent=mode==='cycle'?'Akhir Siklus':'Tanggal Selesai';
  $('[data-period-note]',form).textContent=mode==='cycle'?`Siklus keluarga (tgl ${form.dataset.startDay || 1})`:mode==='all'?'Seluruh catatan keluarga':'Rentang pilihan keluarga';
  $('[data-period-match]',form).textContent=mode==='cycle'?'Tepat 1 Bulan Finansial':mode==='all'?'Seluruh Waktu':'Rentang Kustom';
  $('[data-period-instruction]',form).textContent=start&&!end?'Pilih tanggal selesai untuk melengkapi rentang.':'Pilih tanggal mulai, lalu tanggal selesai.';
  presets.forEach(button => {const selected=button.dataset.periodPreset===activePreset;button.classList.toggle('active',selected);button.setAttribute('aria-pressed',String(selected));});
  const first=new Date(cursor.getFullYear(),cursor.getMonth(),1);
  const offset=(first.getDay()+6)%7;
  const monthDays=new Date(cursor.getFullYear(),cursor.getMonth()+1,0).getDate();
  const gridStart=new Date(first.getFullYear(),first.getMonth(),1-offset);
  const total=Math.ceil((offset+monthDays)/7)*7;
  const last=new Date(gridStart.getFullYear(),gridStart.getMonth(),gridStart.getDate()+total-1);
  let monthLabel=cursor.toLocaleDateString('id-ID',{month:'short',year:'numeric'});
  if(end&&end.getMonth()!==cursor.getMonth()&&start&&start.getMonth()===cursor.getMonth()) {
   monthLabel=cursor.toLocaleDateString('id-ID',{month:'short',...(cursor.getFullYear()!==end.getFullYear()?{year:'numeric'}:{})})+' – '+end.toLocaleDateString('id-ID',{month:'short',year:'numeric'});
  }
  $('[data-period-month]',form).textContent=monthLabel;
  grid.replaceChildren();
  ['Sen','Sel','Rab','Kam','Jum','Sab','Min'].forEach((label,index)=>{const el=document.createElement('span');el.className='period-weekday'+(index===6?' sunday':'');el.textContent=label;grid.append(el);});
  for(let i=0;i<total;i++) {
   const date=new Date(gridStart.getFullYear(),gridStart.getMonth(),gridStart.getDate()+i),value=iso(date);
   const button=document.createElement('button');button.type='button';button.className='period-calendar-day';button.textContent=date.getDate();
   button.classList.toggle('outside',date.getMonth()!==cursor.getMonth());button.classList.toggle('sunday',date.getDay()===0);
   const selected=(start&&value===iso(start))||(end&&value===iso(end));
   button.classList.toggle('selected',!!selected);button.classList.toggle('in-range',!!(start&&end&&value>iso(start)&&value<iso(end)));
   button.setAttribute('aria-label',date.toLocaleDateString('id-ID',{dateStyle:'full'}));button.setAttribute('aria-pressed',String(!!selected));
   button.addEventListener('click',()=>{
    mode='custom';activePreset='custom';
    if(editingEnd&&from.value) {to.value=value;editingEnd=false;}
    else if(!from.value||to.value){from.value=value;to.value='';}
    else to.value=value;
    if(to.value&&to.value<from.value)[from.value,to.value]=[to.value,from.value];
    render();
    const focused=$$('button',grid).find(el=>el.getAttribute('aria-label')===button.getAttribute('aria-label'));focused?.focus();
   });grid.append(button);
  }
  const continuation=$('[data-period-continuation]',form);
  continuation.hidden=!(end&&end>last);
  $('[data-period-continuation-date]',form).textContent=dateLabel(end);
 }
 function setRange(start,end,preset) {from.value=iso(start);to.value=iso(end);mode='custom';activePreset=preset;cursor=new Date(start.getFullYear(),start.getMonth(),1);editingEnd=false;render();}
 function reset() {
  const cycle=presets.find(button=>button.dataset.periodPreset==='cycle');
  if(cycle) {from.value=cycle.dataset.from;to.value=cycle.dataset.to;period.value=cycle.dataset.period;mode='cycle';activePreset='cycle';}
  else {from.value=original.from;to.value=original.to;period.value=original.period;mode=original.mode;activePreset='';}
  const start=parse(from.value)||today;cursor=new Date(start.getFullYear(),start.getMonth(),1);editingEnd=false;render();
 }
 presets.forEach(button=>button.addEventListener('click',()=>{
  const preset=button.dataset.periodPreset;
  if(preset==='cycle'||preset==='previous'){from.value=button.dataset.from;to.value=button.dataset.to;period.value=button.dataset.period;mode='cycle';activePreset=preset;const start=parse(from.value)||today;cursor=new Date(start.getFullYear(),start.getMonth(),1);editingEnd=false;render();return;}
  if(preset==='custom'){mode='custom';activePreset=preset;render();return;}
  const start=new Date(today.getFullYear(),today.getMonth(),today.getDate()),end=new Date(start);
  if(preset==='last7')start.setDate(start.getDate()-6);
  if(preset==='last30')start.setDate(start.getDate()-29);
  if(preset==='month'){start.setDate(1);end.setMonth(end.getMonth()+1,0);}
  if(preset==='year'){start.setMonth(0,1);end.setMonth(11,31);}
  if(preset==='yesterday'){start.setDate(start.getDate()-1);end.setTime(start.getTime());}
  if(preset==='thisWeek'||preset==='lastWeek'){start.setDate(start.getDate()-(start.getDay()+6)%7-(preset==='lastWeek'?7:0));end.setTime(start.getTime());end.setDate(end.getDate()+6);}
  if(preset==='lastMonth'){start.setMonth(start.getMonth()-1,1);end.setDate(0);}
  setRange(start,end,preset);
 }));
 [from,to].forEach(input=>input.addEventListener('click',()=>{try{input.showPicker?.();}catch{}}));
 [from,to].forEach(input=>input.addEventListener('change',()=>{mode='custom';activePreset='custom';if(to.value&&from.value&&to.value<from.value)[from.value,to.value]=[to.value,from.value];const date=parse(input.value);if(date)cursor=new Date(date.getFullYear(),date.getMonth(),1);render();}));
 $('[data-period-prev]',form).addEventListener('click',()=>{cursor.setMonth(cursor.getMonth()-1);render();});
 $('[data-period-next]',form).addEventListener('click',()=>{cursor.setMonth(cursor.getMonth()+1);render();});
 $('[data-period-edit-end]',form).addEventListener('click',()=>{const end=parse(to.value);if(end)cursor=new Date(end.getFullYear(),end.getMonth(),1);editingEnd=true;render();});
 $('[data-period-reset]',form).addEventListener('click',reset);
 form.addEventListener('submit',()=>{if(mode==='cycle'||mode==='all'){from.disabled=to.disabled=true;}else period.disabled=true;});
 $$('[data-open-dialog="period-dialog"]').forEach(button=>button.addEventListener('click',()=>{from.value=original.from;to.value=original.to;period.value=original.period;mode=original.mode;activePreset=mode==='cycle'?(period.value===presets.find(b=>b.dataset.periodPreset==='cycle')?.dataset.period?'cycle':period.value===presets.find(b=>b.dataset.periodPreset==='previous')?.dataset.period?'previous':''):'custom';const start=parse(from.value)||today;cursor=new Date(start.getFullYear(),start.getMonth(),1);editingEnd=false;render();}));
 render();
});

// ---------- menu: menutup sendiri ----------

// <details> mengurus buka-tutupnya sendiri, jadi panel ini tetap bisa dipakai
// tanpa file ini. Yang ditambahkan di sini cuma kebiasaan panel mengambang:
// menutup saat ditekan di luar atau saat Escape. Tanpa itu ia tetap terbuka
// menutupi daftar sampai judulnya ditekan lagi.
$$('.menu').forEach((menu) => {
  document.addEventListener('click', (e) => {
    if (menu.open && !menu.contains(e.target)) menu.open = false;
  });
  menu.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !menu.open) return;
    menu.open = false;
    $('summary', menu).focus();
  });
});

// ---------- PWA ----------

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => navigator.serviceWorker.register('/sw.js').catch(() => {}));
}

// ---------- pencarian simbol di form investasi ----------

// Mengetik kode saham atau nama reksadana memunculkan pilihan lengkap dengan
// namanya. Nama posisinya sendiri tidak diisi di sini melainkan di server, dari
// kode yang tersimpan — dulu diisi dari sini, dan itu balapan yang sering kalah
// karena memilih dari daftar lebih cepat daripada balasan pencariannya sampai.
// Tanpa file ini kolom simbolnya tetap kolom teks biasa.
$$('[data-cari]').forEach((input) => {
  const list = input.list;
  if (!list) return;
  const jenis = input.dataset.cari;
  let tunda = 0;
  let terakhir = '';

  const isiDaftar = (items) => {
    list.replaceChildren();
    for (const it of items) {
      const opt = document.createElement('option');
      opt.value = it.simbol;
      // Label muncul di sebelah nilainya pada peramban desktop; di ponsel ia
      // diabaikan, dan nilainya sendiri sudah cukup menjelaskan.
      opt.label = it.nama + (it.info ? ' — ' + it.info : '');
      list.appendChild(opt);
    }
  };

  const cari = async () => {
    const q = input.value.trim();
    if (q.length < 2 || q === terakhir) return;
    terakhir = q;
    try {
      const r = await fetch('/investasi/cari?jenis=' + encodeURIComponent(jenis) +
        '&q=' + encodeURIComponent(q), { headers: { Accept: 'application/json' } });
      if (!r.ok) return;
      isiDaftar(await r.json());
    } catch {
      // Jaringan sedang tidak bisa dipakai. Kolomnya tetap bisa diketik sendiri,
      // jadi tidak ada yang perlu dikatakan di sini.
    }
  };

  // Ditunda supaya tiap huruf tidak jadi satu permintaan sendiri.
  input.addEventListener('input', () => {
    clearTimeout(tunda);
    tunda = setTimeout(cari, 250);
  });
});

// ---------- form pembelian investasi: harga satuan, total, dan biaya ----------

// Ketiganya terikat satu persamaan: total = kuantitas × harga satuan + biaya.
// Mengubah salah satunya menghitung ulang yang lain, seperti kurs dan biaya
// admin di form transfer. Server menghitungnya lagi dengan aturan yang sama,
// jadi form ini tetap benar tanpa file ini — yang hilang cuma angkanya muncul
// sambil diketik.
(function beliForm() {
  const form = $('[data-beli-form]');
  if (!form) return;

  const qtyEl = $('[data-beli-qty]', form);
  const hargaEl = $('[data-beli-harga]', form);
  const totalEl = $('[data-beli-total]', form);
  const biayaEl = $('[data-beli-biaya]', form);
  if (!qtyEl || !hargaEl || !totalEl || !biayaEl) return;

  const cur = form.dataset.currency || 'IDR';
  // Berapa desimal yang boleh diketik di kolom harga mengikuti mata uangnya —
  // enam untuk rupiah dan dolar, empat untuk yen. Dipasang dari sini supaya
  // template tidak perlu tahu tabel eksponen mata uang.
  hargaEl.dataset.desimal = String(exp(cur) + 4);
  // Kuantitas menyimpan delapan desimal dan harga satuan empat lebih halus
  // dari satuan terkecil mata uangnya; parseNum hanya membaca dua, jadi
  // keduanya butuh pembaca sendiri yang mengikuti ParseQty dan ParsePriceE4.
  const parseDes = (s, maks) => {
    s = String(s || '').replace(/\s/g, '');
    if (!s || /[^\d.,]/.test(s)) return null;
    let whole = s, frac = '';
    const i = Math.max(s.lastIndexOf('.'), s.lastIndexOf(','));
    if (i >= 0) {
      const d = s.length - i - 1;
      const ribuan = s[i] === '.' && d === 3;
      if (d >= 1 && d <= maks && !ribuan) { whole = s.slice(0, i); frac = s.slice(i + 1); }
    }
    whole = whole.replace(/[.,]/g, '');
    if (!/^\d*$/.test(whole) || !/^\d*$/.test(frac)) return null;
    if (whole === '' && frac === '') return null;
    const v = Number((whole || '0') + '.' + (frac || '0'));
    return Number.isFinite(v) ? v : null;
  };
  const qty = () => parseDes(qtyEl.value, 8);
  const harga = () => parseDes(hargaEl.value, exp(cur) + 4);
  const total = () => parseNum(totalEl.value);

  // Yang terakhir disentuh orangnya menang; yang satunya lagi yang dihitung.
  // Tanpa aturan ini keduanya saling menimpa dan tidak ada yang bisa diketik.
  let derive = 'biaya';

  // fmtQty menulis kuantitas dengan delapan desimalnya, tanpa nol di ekor —
  // cerminan FormatQty di investasi.go. fmtPlain membulatkan ke satuan terkecil
  // mata uang, dan pecahan unit yang didapat dari nominal bulat justru hilang
  // di situ.
  function fmtQty(v) {
    const [w, f = ''] = v.toFixed(8).split('.');
    const ekor = f.replace(/0+$/, '');
    return w.replace(/\B(?=(\d{3})+(?!\d))/g, '.') + (ekor ? ',' + ekor : '');
  }

  // Biaya negatif tidak dikosongkan diam-diam: ia berarti harga dikali
  // kuantitas melebihi totalnya, dan menyembunyikannya cuma menunda kabar
  // yang tetap akan datang dari server.
  function tulis(el, v) {
    const baru = v === null ? '' : fmtPlain(v, cur);
    if (el.value !== baru) el.value = baru;
  }

  function recalc() {
    const q = qty(), h = harga(), t = total();

    // Total masih kosong: diisikan dari kuantitas dikali harga. Itu tebakan
    // yang hampir selalu benar, dan yang membelinya dengan komisi tinggal
    // membetulkan totalnya — selisihnya lalu jatuh ke biaya sendiri.
    if (q !== null && h !== null && totalEl.value.trim() === '') {
      tulis(totalEl, q * h);
      derive = 'biaya';
    }

    // Biaya yang diketik sendiri menghitung kuantitasnya: "habis segini,
    // komisinya segini, di harga segini — dapat berapa" adalah cara emas dan
    // reksadana dibeli, dengan nominal bulat dan kuantitas yang baru ketahuan
    // belakangan. Biaya nol dihitung, bukan diabaikan: nol adalah jawaban, dan
    // kolom kosong yang belum dijawab yang tidak menghitung apa-apa.
    if (derive === 'qty') {
      const t2 = total(), b = parseNum(biayaEl.value);
      if (h !== null && h > 0 && t2 !== null && b !== null && t2 - b > 0) {
        qtyEl.value = fmtQty((t2 - b) / h);
      }
      return;
    }
    const t3 = total();
    if (q === null || h === null || t3 === null) return;
    tulis(biayaEl, t3 - q * h);
  }

  qtyEl.addEventListener('input', () => { derive = 'biaya'; recalc(); });
  hargaEl.addEventListener('input', () => { derive = 'biaya'; recalc(); });
  totalEl.addEventListener('input', () => { derive = 'biaya'; recalc(); });
  biayaEl.addEventListener('input', () => { derive = 'qty'; recalc(); });
})();

// Native disclosure keeps Catat usable without JavaScript.
$$('[data-close-catat]').forEach((button) => button.addEventListener('click', () => {
  const menu = button.closest('details');
  menu.open = false;
  $('summary', menu).focus();
}));

$$('[data-copy]').forEach((button) => button.addEventListener('click', async () => {
  const input = document.getElementById(button.dataset.copy);
  try {
    await navigator.clipboard.writeText(input.value);
    button.textContent = 'Tersalin';
  } catch {
    input.focus();
    input.select();
    button.textContent = 'Salin teks terpilih';
  }
}));
