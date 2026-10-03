// UI enhancements for Stitch compositions; money is validated by the server.
function toast(message) { const el = $('.toast'); if (!el) return; el.textContent = message; el.hidden = false; clearTimeout(el.hideTimer); el.hideTimer = setTimeout(() => { el.hidden = true; }, 3500); }
let dialogOpener = null;
$$('[data-open-dialog]').forEach(button => button.addEventListener('click', () => {
 const dialog = document.getElementById(button.dataset.openDialog); if (!dialog) return;
 dialogOpener = button;
 if (dialog.id === 'catat-dialog') { const frame = $('[data-catat-frame]', dialog); if (!frame.src) frame.src = '/transaksi/baru?jenis=expense&sheet=1'; }
 if (dialog.id === 'notifications-dialog') loadNotifications();
 dialog.showModal();
}));
$$('[data-close-dialog]').forEach(button => button.addEventListener('click', () => button.closest('dialog')?.close()));
$$('dialog').forEach(dialog => {
 dialog.addEventListener('click', event => { if (event.target === dialog) { const rect = dialog.getBoundingClientRect(); if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) dialog.close(); } });
 dialog.addEventListener('close', () => dialogOpener?.focus());
});
if (document.body.classList.contains('in-sheet')) $$('a').forEach(link => { if(link.closest('.seg')) { const url = new URL(link.href); url.searchParams.set('sheet', '1'); link.href = url.href; } else { link.target = '_top'; } });

$$('[data-filter-group]').forEach(group => {
 const name = group.dataset.filterGroup;
 $$('button[data-filter]', group).forEach(button => {
  button.setAttribute('aria-pressed', String(button.classList.contains('active')));
  button.addEventListener('click', () => { $$('button', group).forEach(b => { b.classList.toggle('active', b === button); b.setAttribute('aria-pressed', String(b === button)); }); $$(`[data-filter-item="${name}"]`).forEach(item => { item.hidden = button.dataset.filter !== 'all' && item.dataset.filterValue !== button.dataset.filter; }); $$(`[data-filter-section="${name}"]`).forEach(section => { section.hidden = !$$(`[data-filter-item="${name}"]`, section).some(item => !item.hidden); }); });
 });
});
$('[data-sort-positions]')?.addEventListener('change', event => {
 const list = $('.positions-list'); const cards = $$('[data-filter-item="positions"]', list);
 cards.sort((a, b) => event.target.value === 'name' ? a.dataset.name.localeCompare(b.dataset.name, 'id') : Number(b.dataset.value) - Number(a.dataset.value)); cards.forEach(card => list.append(card));
});
function syncWalletPicker(picker) { const select = $('select', picker); const option = select?.selectedOptions[0]; if (!option) return; $('[data-wallet-name]', picker).textContent = option.dataset.name || option.textContent; $('[data-wallet-balance]', picker).textContent = option.dataset.balance || ''; }
$$('.wallet-picker').forEach(picker => { syncWalletPicker(picker); $('select', picker).addEventListener('change', () => syncWalletPicker(picker)); });
$$('.date-display').forEach(label => { const input = $('input', label); const update = () => { if (!input.value) return; const [year, month, day] = input.value.split('-').map(Number); $('[data-date-label]', label).textContent = new Intl.DateTimeFormat('id-ID', { day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(year, month - 1, day)); }; input.addEventListener('change', update); update(); });
const receiptMaxBytes = 2 * 1024 * 1024;
async function compressReceiptImage(file) {
 // Keep small originals intact; large camera photos are resized and encoded as JPEG.
 if (file.size <= receiptMaxBytes) return file;
 const url = URL.createObjectURL(file);
 const image = new Image();
 try {
  await new Promise((resolve, reject) => { image.onload = resolve; image.onerror = () => reject(new Error('Foto tidak dapat dibaca. Gunakan JPG, PNG atau WebP.')); image.src = url; });
  let scale = Math.min(1, 2400 / Math.max(image.naturalWidth, image.naturalHeight));
  const canvas = document.createElement('canvas');
  const context = canvas.getContext('2d');
  if (!context || !image.naturalWidth || !image.naturalHeight) throw new Error('Foto tidak dapat diproses di browser ini.');
  for (let attempt = 0; attempt < 6; attempt++) {
   canvas.width = Math.max(1, Math.round(image.naturalWidth * scale));
   canvas.height = Math.max(1, Math.round(image.naturalHeight * scale));
   // White backing preserves legibility for transparent PNG receipts.
   context.fillStyle = '#fff'; context.fillRect(0, 0, canvas.width, canvas.height);
   context.drawImage(image, 0, 0, canvas.width, canvas.height);
   for (const quality of [0.88, 0.75, 0.6]) {
    const blob = await new Promise(resolve => canvas.toBlob(resolve, 'image/jpeg', quality));
    if (!blob) throw new Error('Foto gagal dikompres. Coba pilih foto lain.');
    if (blob.size <= receiptMaxBytes) {
     return new File([blob], `${file.name.replace(/\.[^.]+$/, '') || 'struk'}.jpg`, { type: 'image/jpeg', lastModified: file.lastModified });
    }
   }
   scale *= 0.75;
  }
  throw new Error('Foto masih terlalu besar setelah dikompres. Coba foto dengan resolusi lebih kecil.');
 } finally { URL.revokeObjectURL(url); }
}
$$('[data-receipt-input]').forEach(input => {
 let pending = null, revision = 0, waitingSubmission = false;
 const form = input.form;
 const nameLabel = $('[data-receipt-name]', input.closest('label'));
 const summary = $('[data-receipt-summary]', form);
 const display = message => { nameLabel.textContent = message || 'Foto dikompres otomatis · PDF maksimal 2 MB'; if (summary) { summary.hidden = !message; summary.textContent = message || ''; } };
 input.addEventListener('change', () => {
  const file = input.files?.[0], current = ++revision;
  input.setCustomValidity('');
  if (!file) { pending = null; display(''); return; }
  display('Menyiapkan lampiran…');
  const task = (async () => {
   try {
    let upload = file;
    if (file.size > receiptMaxBytes) {
     if (!file.type.startsWith('image/')) throw new Error('PDF maksimal 2 MB. Pilih PDF dengan ukuran lebih kecil.');
     upload = await compressReceiptImage(file);
     if (current !== revision) return;
     const transfer = new DataTransfer(); transfer.items.add(upload); input.files = transfer.files;
     if (input.files[0]?.size !== upload.size || input.files[0]?.name !== upload.name) throw new Error('Foto hasil kompresi belum dapat dilampirkan. Coba browser lain.');
    }
    if (current === revision) display(upload.name);
   } catch (error) {
    if (current !== revision) return;
    input.value = ''; input.setCustomValidity(error.message); display(error.message); toast(error.message);
   }
  })();
  pending = task;
  task.finally(() => { if (current === revision) pending = null; });
 });
 // Enter or tapping Save must never upload the original while compression is running.
 form.addEventListener('submit', async event => {
  if (!pending && !waitingSubmission) return;
  event.preventDefault();
  if (waitingSubmission) return;
  waitingSubmission = true; const submitter = event.submitter;
  try {
   while (pending) await pending;
  } finally { waitingSubmission = false; }
  if (form.reportValidity()) form.requestSubmit(submitter || undefined);
 });
});

(() => {
 const form = $('.expense-form'); if (!form) return;
 const amount = $('#nominal', form), wallet = $('#dompet', form), toggle = $('[data-installment-toggle]', form), fields = $('.installment-fields', form);
 const mode = $('[data-installment-mode]', form), duration = $('[name="cicilan"]', form);
 const footer = $('.save-footer', form), main = form.closest('main');
 if (footer && main) {
  const reserveFooter = () => { main.style.paddingBottom = `${Math.ceil(footer.getBoundingClientRect().height) + 24}px`; };
  reserveFooter();
  if (typeof ResizeObserver !== 'undefined') new ResizeObserver(reserveFooter).observe(footer);
 }
 const symbol = () => symOf(wallet), currency = () => curOf(wallet);
 let timer, controller, revision = 0, budgetKey = "";
 const status = $('[data-budget-state]', form);
 const showBudget = (state, message) => { status.dataset.status = state; status.textContent = message; };
 const scheduleBudget = () => {
  if (form.dataset.kind !== 'expense') { status.hidden = true; return; }
  const category = $('[name="kategori"]:checked', form)?.value;
  const key = JSON.stringify([category, amount.value, wallet.value, $('[name="tanggal"]', form).value, toggle?.checked, mode?.value, duration?.value]);
  if (key === budgetKey) return; budgetKey = key;
  clearTimeout(timer); controller?.abort(); const current = ++revision;
  if (!category) { showBudget('', 'Pilih kategori untuk melihat anggaran'); return; }
  showBudget('', 'Memeriksa anggaran…');
  timer = setTimeout(async () => {
   controller = new AbortController();
   const params = new URLSearchParams({ kategori: category, nominal: amount.value || '0', dompet: wallet.value, tanggal: $('[name="tanggal"]', form).value, id: form.dataset.editId || '0' });
   if (toggle?.checked) { params.set('mode', mode.value); params.set('cicilan', duration.value); }
   try {
    const response = await fetch(`/anggaran/evaluasi?${params}`, { signal: controller.signal, cache: 'no-store', headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error();
    const result = await response.json(); if (current === revision) showBudget(result.status, result.message);
   } catch (error) { if (error.name !== 'AbortError' && current === revision) { budgetKey = ''; showBudget('', 'Anggaran belum dapat diperiksa.'); } }
  }, 180);
 };
 const update = () => {
  const n = parseNum(amount.value) || 0;
  amount.style.width = `${Math.max(1, (amount.value || amount.placeholder || '0').length) + .5}ch`;
  const formatted = `${symbol()} ${fmtPlain(n, currency())}`;
  $('[data-live-amount]', form).textContent = formatted;
  if (toggle?.checked) { const months = Math.max(2, Number(duration.value) || 3); $('[data-installment-preview]', form).textContent = `${symbol()} ${fmtPlain(mode.value === 'ulang' ? n : n / months, currency())}`; $('[data-installment-help]', form).textContent = mode.value === 'ulang' ? 'Nominal penuh dicatat setiap bulan sesuai durasi yang dipilih.' : 'Pencatatan dibagi rata per bulan ke kategori terkait tanpa membebani arus kas bulan ini sekaligus.'; }
  scheduleBudget();
 };
 toggle?.addEventListener('change', () => { fields.hidden = !toggle.checked; fields.closest('.installment-card').hidden = !toggle.checked; $$('input,select', fields).forEach(input => input.disabled = !toggle.checked); update(); });
 $$('[data-add-amount]', form).forEach(button => button.addEventListener('click', () => { amount.value = fmtPlain((parseNum(amount.value) || 0) + Number(button.dataset.addAmount), currency()); amount.dispatchEvent(new Event('input', { bubbles: true })); }));
 const recorder = () => { const selected = $('[name="pencatat"]:checked', form); const label = $('[data-recorder-label]', form); if (selected && label) label.textContent = $('strong', selected.closest('label')).textContent.replace(' (Saya)', ''); };
 form.addEventListener('input', update); form.addEventListener('change', () => { recorder(); update(); }); recorder(); update();
})();
(() => {
 const form = $('#transfer-form'); if (!form) return;
 const from = $('[data-from]', form), to = $('[data-to]', form), amount = $('#nominal_keluar', form), received = $('#nominal_diterima', form);
 const update = () => requestAnimationFrame(() => {
  const out = parseNum(amount.value) || 0; const incoming = parseNum(received.value) || 0; const option = from.selectedOptions[0];
  $('[data-transfer-out]', form).textContent = `−${symOf(from)} ${fmtPlain(out, curOf(from))}`;
  $('[data-transfer-in]', form).textContent = `+${symOf(to)} ${fmtPlain(incoming, curOf(to))}`;
  const balance = Number(option?.dataset.minor || 0) / (10 ** exp(curOf(from)));
  const state = $('[data-transfer-balance]', form); state.textContent = out > balance ? 'Nominal melebihi saldo dompet' : out > 0 ? '✓ Saldo mencukupi untuk transfer' : 'Masukkan nominal transfer'; state.classList.toggle('m-out', out > balance);
 });
 $('[data-transfer-max]', form)?.addEventListener('click', () => { amount.value = fmtPlain(Math.max(0, Number(from.selectedOptions[0]?.dataset.minor || 0) / (10 ** exp(curOf(from)))), curOf(from)); amount.dispatchEvent(new Event('input', { bubbles: true })); });
 form.addEventListener('input', update); form.addEventListener('change', update); update();
})();

const reminderKey = `rukun-reminders:${document.body.dataset.family || location.host}`;
function reminders() { try { return JSON.parse(localStorage.getItem(reminderKey) || '{}'); } catch { return {}; } }
function remind(id) { const saved = reminders(); const tomorrow = new Date(); tomorrow.setHours(24, 0, 0, 0); saved[id] = tomorrow.getTime(); try { localStorage.setItem(reminderKey, JSON.stringify(saved)); } catch { toast('Pengingat tidak dapat disimpan di browser ini.'); return; } $$('[data-reminder-card]').filter(card => card.dataset.reminderCard === id).forEach(card => card.hidden = true); toast('Tagihan akan ditampilkan kembali besok di perangkat ini.'); }
$$('[data-remind]').forEach(button => button.addEventListener('click', () => remind(button.dataset.remind)));
$$('[data-reminder-card]').forEach(card => { if (Number(reminders()[card.dataset.reminderCard]) > Date.now()) card.hidden = true; });
async function loadNotifications() {
 const container = $('[data-notifications]'); if (!container) return;
 try {
  const response = await fetch('/notifikasi', { headers: { Accept: 'application/json' } }); if (!response.ok) throw new Error(); const notes = await response.json(); container.replaceChildren();
  const visible = notes.filter(note => !(Number(reminders()[note.ID]) > Date.now()));
  if (!visible.length) { const p = document.createElement('p'); p.className = 'text-muted'; p.textContent = 'Tidak ada tagihan yang perlu perhatian dalam tujuh hari ke depan.'; container.append(p); }
  visible.forEach(note => { const card = document.createElement('div'); card.className = 'card'; const link = document.createElement('a'); link.href = note.Href; link.textContent = note.Title; const details = document.createElement('p'); details.className = 'small text-muted'; details.textContent = `${note.Description} · ${note.Amount}`; const button = document.createElement('button'); button.className = 'btn btn-secondary'; button.textContent = 'Ingatkan Besok'; button.addEventListener('click', () => { remind(note.ID); card.remove(); }); card.append(link, details, button); container.append(card); });
 } catch { container.textContent = 'Tagihan belum dapat dimuat. Periksa koneksi lalu buka kembali.'; }
}
$$('[data-share]').forEach(button => button.addEventListener('click', async () => { const data = { title: 'Catatan keuangan keluarga', text: `${$('.detail-head h3')?.textContent || ''} · ${$('.detail-head .big-figure')?.textContent || ''}`, url: location.href }; try { if (navigator.share) await navigator.share(data); else { await navigator.clipboard.writeText(`${data.text}\n${data.url}`); toast('Tautan transaksi tersalin.'); } } catch (error) { if (error.name !== 'AbortError') toast('Tautan belum dapat dibagikan.'); } }));
$('[data-voice-search]')?.addEventListener('click', () => { const Speech = window.SpeechRecognition || window.webkitSpeechRecognition; if (!Speech) { toast('Browser ini belum mendukung pencarian suara.'); $('#cari')?.focus(); return; } const speech = new Speech(); speech.lang = 'id-ID'; speech.onresult = event => { $('#cari').value = event.results[0][0].transcript; $('#cari').form.requestSubmit(); }; speech.onerror = () => toast('Suara belum dapat dibaca. Coba ketik pencarian.'); speech.start(); });
let installPrompt;
window.addEventListener('beforeinstallprompt', event => { event.preventDefault(); installPrompt = event; $$('[data-install-pwa]').forEach(button => button.hidden = false); });
$$('[data-install-pwa]').forEach(button => button.addEventListener('click', async () => { if (!installPrompt) return; await installPrompt.prompt(); installPrompt = null; button.hidden = true; }));
function connectionStatus() { const online = navigator.onLine; document.body.classList.toggle('offline', !online); $$('[data-connection-label]').forEach(label => { label.textContent = online ? (label.closest('.footer-summary') ? 'Siap dibagikan' : 'Online') : 'Offline'; }); const installed = matchMedia('(display-mode: standalone)').matches || navigator.standalone; const status = $('[data-pwa-status]'); if (status) status.textContent = `${installed ? 'Terpasang di layar utama' : 'Dibuka melalui browser'} · ${online ? 'Online' : 'Offline'}`; }
window.addEventListener('online', connectionStatus); window.addEventListener('offline', connectionStatus); window.addEventListener('appinstalled', connectionStatus); connectionStatus();
// Poll a database revision; never replace a form while someone is entering data.
if (document.body.dataset.family && document.body.dataset.revision && !document.body.classList.contains('in-sheet')) {
 let revision = Number(document.body.dataset.revision);
 async function syncFamily() {
  if (!navigator.onLine || document.hidden) return;
  try {
   const response = await fetch('/sinkronisasi', { cache: 'no-store', headers: { Accept: 'application/json' } }); if (!response.ok) return;
   const state = await response.json();
   if (state.revision !== revision) { const editing = !!document.querySelector('form.ledger-form,form[data-dirty],dialog[open]') || document.activeElement?.matches('input,textarea,select'); if (editing) { $('.update-banner').hidden = false; } else { location.reload(); } }
   else $$('[data-connection-label]').forEach(label => { if (!label.closest('.footer-summary')) label.textContent = 'Sinkron'; });
  } catch { /* The rendered snapshot stays visible until the connection recovers. */ }
 }
 document.addEventListener('input', event => { const form = event.target.closest('form'); if (form?.method === 'post') form.dataset.dirty = 'true'; });
 setInterval(syncFamily, 30000); window.addEventListener('online', syncFamily); document.addEventListener('visibilitychange', syncFamily); syncFamily();
}

$('[data-dismiss-debt-note]')?.addEventListener('click', event => { event.currentTarget.closest('[data-debt-note]').hidden = true; });
