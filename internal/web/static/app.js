// Sherwood's only page script. It registers the service worker, which keeps
// the app's shell (styles, script, manifest and icons) so the installed app
// opens quickly; the worker never stores a page or any data. On Settings it
// also turns on notifications: it asks the browser for a push subscription
// and posts it to sherwood with the page's anti-forgery token.
'use strict';

if ('serviceWorker' in navigator) {
  window.addEventListener('load', function () {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(function () {
      // Without the worker the app still works; it just fetches its shell
      // from the NAS each time.
    });
  });
}

// base64url text to bytes, for the VAPID public key.
function fromBase64Url(text) {
  const padded = text.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((text.length + 3) % 4);
  const raw = atob(padded);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) {
    bytes[i] = raw.charCodeAt(i);
  }
  return bytes;
}

// bytes to base64url text without padding, for the subscription's keys.
function toBase64Url(buffer) {
  const bytes = new Uint8Array(buffer);
  let raw = '';
  for (let i = 0; i < bytes.length; i++) {
    raw += String.fromCharCode(bytes[i]);
  }
  return btoa(raw).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function sameBytes(a, b) {
  if (!a || !b) {
    return false;
  }
  const x = new Uint8Array(a);
  const y = new Uint8Array(b);
  return x.length === y.length && x.every(function (v, i) { return v === y[i]; });
}

// subscribe returns this device's push subscription for key, replacing one
// made with an older key.
function subscribe(registration, key) {
  return registration.pushManager.getSubscription().then(function (existing) {
    if (existing && sameBytes(existing.options.applicationServerKey, key)) {
      return existing;
    }
    const fresh = function () {
      return registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
    };
    return existing ? existing.unsubscribe().then(fresh) : fresh();
  });
}

(function setUpNotifications() {
  const form = document.getElementById('push-subscribe');
  if (!form) {
    return;
  }
  const status = document.getElementById('push-status');
  const say = function (text) {
    if (status) {
      status.textContent = text;
    }
  };
  if (!('serviceWorker' in navigator) || !('PushManager' in window) || !('Notification' in window)) {
    say('This browser cannot show notifications here. On iPhone, add sherwood to your home screen and open it from there.');
    form.querySelector('button').disabled = true;
    return;
  }
  let busy = false;
  form.addEventListener('submit', function (event) {
    event.preventDefault();
    if (busy) {
      return;
    }
    busy = true;
    say('Asking the phone for permission…');
    // The permission request must start inside the tap, so it comes first.
    Notification.requestPermission()
      .then(function (permission) {
        if (permission !== 'granted') {
          throw new Error('blocked');
        }
        return navigator.serviceWorker.ready;
      })
      .then(function (registration) {
        return subscribe(registration, fromBase64Url(form.dataset.key));
      })
      .then(function (subscription) {
        form.elements.endpoint.value = subscription.endpoint;
        form.elements.p256dh.value = toBase64Url(subscription.getKey('p256dh'));
        form.elements.auth.value = toBase64Url(subscription.getKey('auth'));
        say('Saving…');
        form.submit();
      })
      .catch(function (err) {
        busy = false;
        say(err && err.message === 'blocked'
          ? 'Notifications are blocked for sherwood. Allow them in the phone\'s settings, then try again.'
          : 'Notifications could not be turned on. Try again.');
      });
  });
})();
