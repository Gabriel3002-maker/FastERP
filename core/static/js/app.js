// FastERP Core HTMX App
console.log('FastERP loaded');

// Configurar defaults de HTMX
htmx.config.useWebSockets = true;
htmx.config.timeout = 10000;

// Logging
htmx.on('htmx:xhr:loadend', function(evt) {
  console.log('Request complete:', evt.detail.xhr.status);
});
