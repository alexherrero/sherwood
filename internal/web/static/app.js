// Sherwood's only page script: it registers the service worker, which keeps
// the app's shell (styles, script, manifest and icons) so the installed app
// opens quickly. The worker never stores a page or any data.
'use strict';

if ('serviceWorker' in navigator) {
  window.addEventListener('load', function () {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(function () {
      // Without the worker the app still works; it just fetches its shell
      // from the NAS each time.
    });
  });
}
