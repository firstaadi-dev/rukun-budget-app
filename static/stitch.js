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
  button.addEventListener('click', () => { $$('button', group).forEach(b => { b.classList.toggle('active', b === button); b.setAttribute('aria-pressed', String(b === button)); }); $$(`[data-filter-item="${name}"]`).forEach(item => { item.hidden = button.dataset.filter !== 'all' && item.dataset.filterValue !== button.dataset.filter; }); });
 });
});
$('[data-sort-positions]')?.addEventListener('change', event => {
 const list = $('.positions-list'); const cards = $$('[data-filter-item="positions"]', list);
 cards.sort((a, b) => event.target.value === 'name' ? a.dataset.name.localeCompare(b.dataset.name, 'id') : Number(b.dataset.value) - Number(a.dataset.value)); cards.forEach(card => list.append(card));
});
function syncWalletPicker(picker) { const select = $('select', picker); const option = select?.selectedOptions[0]; if (!option) return; $('[data-wallet-name]', picker).textContent = option.dataset.name || option.textContent; $('[data-wallet-balance]', picker).textContent = option.dataset.balance || ''; }
$$('.wallet-picker').forEach(picker => { syncWalletPicker(picker); $('select', picker).addEventListener('change', () => syncWalletPicker(picker)); });
$$('.date-display').forEach(label => { const input = $('input', label); const update = () => { if (!input.value) return; const [year, month, day] = input.value.split('-').map(Number); $('[data-date-label]', label).textContent = new Intl.DateTimeFormat('id-ID', { day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(year, month - 1, day)); }; input.addEventListener('change', update); update(); });
$$('[data-receipt-input]').forEach(input => input.addEventListener('change', () => { const file = input.files?.[0]; if (file && file.size > 2 * 1024 * 1024) { toast('Struk maksimal 2 MB.'); input.value = ''; } $('[data-receipt-name]', input.closest('label')).textContent = input.files?.[0]?.name || 'JPG, PNG, WebP atau PDF · maksimal 2 MB'; }));

(() => {
 const form = $('.expense-form'); if (!form) return;
 const amount = $('#nominal', form), wallet = $('#dompet', form), toggle = $('[data-installment-toggle]', form), fields = $('.installment-fields', form);
 const mode = $('[data-installment-mode]', form), duration = $('[name="cicilan"]', form);
 const symbol = () => symOf(wallet), currency = () => curOf(wallet);
 const update = () => {
  const n = parseNum(amount.value) || 0;
  const formatted = `${symbol()} ${fmtPlain(n, currency())}`;
  $('[data-live-amount]', form).textContent = formatted;
  if (toggle?.checked) { const months = Math.max(2, Number(duration.value) || 3); $('[data-installment-preview]', form).textContent = `(${symbol()} ${fmtPlain(mode.value === 'ulang' ? n : n / months, currency())} / bln)`; $('[data-installment-help]', form).textContent = mode.value === 'ulang' ? 'Nominal penuh dicatat setiap bulan sesuai durasi yang dipilih.' : 'Pencatatan dibagi rata per bulan ke kategori terkait tanpa membebani arus kas bulan ini sekaligus.'; }
  let rows = []; try { rows = JSON.parse($('[data-budget-json]')?.dataset.budgetJson || '[]') || []; } catch {}
  const category = $('[name="kategori"]:checked', form)?.value;
  const row = rows.find(r => r.Name === category); const status = $('[data-budget-state]', form);
  if (!category) { status.textContent = 'Pilih kategori untuk melihat anggaran'; status.classList.remove('m-out'); return; }
  if (!row || currency() !== (document.body.dataset.base || 'IDR')) { status.textContent = row ? 'Anggaran diperiksa pada catatan keluarga' : 'Kategori fleksibel · belum ada batas anggaran'; status.classList.remove('m-out'); return; }
  const left = parseNum(row.Remaining) || 0; const charge = toggle?.checked && mode.value === 'cicil' ? n / Math.max(2, Number(duration.value) || 3) : n;
  status.textContent = charge > left ? 'Melewati sisa anggaran kategori' : '✓ Sesuai pos anggaran bulanan'; status.classList.toggle('m-out', charge > left);
 };
 toggle?.addEventListener('change', () => { fields.hidden = !toggle.checked; $$('input,select', fields).forEach(input => input.disabled = !toggle.checked); update(); });
 $$('[data-add-amount]', form).forEach(button => button.addEventListener('click', () => { amount.value = fmtPlain((parseNum(amount.value) || 0) + Number(button.dataset.addAmount), currency()); amount.dispatchEvent(new Event('input', { bubbles: true })); }));
 form.addEventListener('input', update); form.addEventListener('change', update); update();
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
