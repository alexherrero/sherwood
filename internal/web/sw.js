// Sherwood's service worker. It caches the app's shell and nothing else:
// the styles, this app's script, the manifest and the icons. Pages and data
// always come fresh from the NAS, so a view is never shown from a saved copy
// and no buy is ever approved from stale numbers.
//
// The server fills in VERSION from a hash of the shell files. When any of
// them changes, this file changes too, the phone installs the new worker, and
// the old cache is deleted.
'use strict';

const VERSION = '__SHELL_VERSION__';
const CACHE = 'sherwood-shell-' + VERSION;

// SHELL is the whole cache list. A test reads it from the served file and
// refuses any entry that is a view or another data route.
const SHELL = [
  '/static/app.css',
  '/static/app.js',
  '/static/manifest.webmanifest',
  '/static/icon.svg',
  '/static/icon-192.png',
  '/static/icon-512.png',
  '/static/apple-touch-icon.png',
];

self.addEventListener('install', function (event) {
  event.waitUntil(
    caches.open(CACHE)
      .then(function (cache) { return cache.addAll(SHELL); })
      .then(function () { return self.skipWaiting(); })
  );
});

self.addEventListener('activate', function (event) {
  event.waitUntil(
    caches.keys()
      .then(function (keys) {
        return Promise.all(keys
          .filter(function (key) { return key !== CACHE; })
          .map(function (key) { return caches.delete(key); }));
      })
      .then(function () { return self.clients.claim(); })
  );
});

self.addEventListener('fetch', function (event) {
  const request = event.request;
  if (request.method !== 'GET') {
    return; // actions always go to the network, untouched
  }
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) {
    return;
  }
  if (url.search === '' && SHELL.includes(url.pathname)) {
    event.respondWith(
      caches.open(CACHE)
        .then(function (cache) { return cache.match(url.pathname); })
        .then(function (hit) { return hit || fetch(request); })
    );
    return;
  }
  if (request.mode === 'navigate') {
    // A page comes from the network every time. Offline, the app says so
    // instead of showing anything saved.
    event.respondWith(fetch(request).catch(offline));
  }
  // Anything else goes to the network and is never stored.
});

function offline() {
  const body = '<!doctype html><html lang="en"><head><meta charset="utf-8">' +
    '<meta name="viewport" content="width=device-width, initial-scale=1">' +
    '<title>Offline · Sherwood</title><link rel="stylesheet" href="/static/app.css"></head>' +
    '<body><main id="main"><h1>Sherwood can\'t reach the NAS</h1>' +
    '<p class="notice"><strong>You are offline.</strong> Sherwood never shows a saved copy of your data. ' +
    'Check that the phone is on your tailnet, then try again.</p>' +
    '<p><a class="button" href="/">Try again</a></p></main></body></html>';
  return new Response(body, {
    status: 503,
    headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' },
  });
}
