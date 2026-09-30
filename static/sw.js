// Only the public offline screen and icon are cached. Family data stays network-only.
const OFFLINE_CACHE = 'rukun-offline-v1';
self.addEventListener('install', event => { event.waitUntil(caches.open(OFFLINE_CACHE).then(cache => cache.addAll(['/static/offline.html', '/static/icon.svg'])).then(() => self.skipWaiting())); });
self.addEventListener('activate', event => { event.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(key => key !== OFFLINE_CACHE && key.startsWith('rukun-offline-')).map(key => caches.delete(key)))).then(() => self.clients.claim())); });
self.addEventListener('fetch', event => {
 if (event.request.mode === 'navigate') event.respondWith(fetch(event.request).catch(() => caches.match('/static/offline.html')));
 else if (new URL(event.request.url).pathname === '/static/icon.svg') event.respondWith(fetch(event.request).catch(() => caches.match('/static/icon.svg')));
});
