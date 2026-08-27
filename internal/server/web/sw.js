// EDT Service Worker - 离线兜底
// 策略：静态资源（/assets/）不做 SW 缓存——由 HTTP ETag 协商缓存负责（避免 SW 缓存导致的旧版 JS 问题）；
// SW 仅拦截页面 HTML：网络优先，离线时回退缓存的同名页面。
const CACHE_NAME = 'edt-v6';

self.addEventListener('install', (event) => {
  self.skipWaiting();
  // 不再预缓存资源：首次访问时由运行时按需缓存 HTML
  event.waitUntil(Promise.resolve());
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((names) => {
      return Promise.all(
        names.filter(n => n !== CACHE_NAME).map(n => caches.delete(n))
      );
    }).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  if (event.request.method !== 'GET') return;
  const url = new URL(event.request.url);
  // 仅处理同源页面导航；API / 静态资源 / WS 全部直连（HTTP 缓存已够用）
  if (url.origin !== location.origin) return;
  const isHTML = event.request.headers.get('accept')?.includes('text/html');
  const isPage = ['/', '/nodes', '/selector', '/settings'].includes(url.pathname);
  if (!isHTML || !isPage) return;

  event.respondWith(
    fetch(event.request).then((resp) => {
      if (resp.ok) {
        const clone = resp.clone();
        caches.open(CACHE_NAME).then(cache => cache.put(event.request, clone));
      }
      return resp;
    }).catch(() => {
      return caches.match(event.request).then((cached) => {
        return cached || caches.match('/').then((root) => root || new Response('Offline', { status: 503, headers: { 'Content-Type': 'text/plain; charset=utf-8' } }));
      });
    })
  );
});
