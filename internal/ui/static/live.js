// galdor scry — live updates for the run list.
//
// Subscribes to /api/stream/runs (Server-Sent Events) and inserts
// new run rows at the top of the table. Existing rows for the
// same run are updated in place. The script is self-contained and
// dependency-free; if EventSource isn't available the static page
// keeps working unchanged.

(function () {
  if (typeof EventSource !== 'function') return;

  var table = document.querySelector('table.runs tbody');
  var indicator = document.getElementById('live-indicator');
  if (!table) return;

  var src = new EventSource('/api/stream/runs');

  src.addEventListener('open', function () {
    if (indicator) indicator.dataset.state = 'connected';
  });

  src.addEventListener('error', function () {
    if (indicator) indicator.dataset.state = 'disconnected';
  });

  src.addEventListener('heartbeat', function () {
    if (indicator) indicator.dataset.state = 'connected';
  });

  src.addEventListener('run', function (ev) {
    var run;
    try { run = JSON.parse(ev.data); } catch (_) { return; }
    upsertRow(run);
  });

  function upsertRow(run) {
    var existing = document.querySelector(
      'tr[data-run-id="' + cssEscape(run.RunID) + '"]'
    );
    var row = buildRow(run);
    if (existing) {
      existing.replaceWith(row);
    } else {
      table.insertBefore(row, table.firstChild);
      row.classList.add('flash');
      setTimeout(function () { row.classList.remove('flash'); }, 1500);
    }
  }

  function buildRow(run) {
    var status = run.ErrorCount > 0 ? 'error' : 'ok';
    var duration = formatDuration(run.EndTimeUnixNano - run.StartTimeUnixNano);
    var started = run.StartTimeUnixNano
      ? new Date(run.StartTimeUnixNano / 1e6).toISOString()
      : '—';

    var tr = document.createElement('tr');
    tr.setAttribute('data-run-id', run.RunID);
    tr.innerHTML =
      '<td><a href="/runs/' + escapeHTML(run.RunID) + '" class="mono">' +
        escapeHTML(run.RunID) + '</a></td>' +
      '<td><span class="badge ' + (status === 'ok' ? 'ok' : 'err') + '">' +
        status + '</span></td>' +
      '<td class="num">' + duration + '</td>' +
      '<td class="num">' + (run.SpanCount || 0) + '</td>' +
      '<td class="num">' + (run.ErrorCount || 0) + '</td>' +
      '<td class="mono dim">' + escapeHTML(started) + '</td>';
    return tr;
  }

  function formatDuration(nanos) {
    if (!nanos || nanos <= 0) return '—';
    if (nanos < 1e3) return nanos + 'ns';
    if (nanos < 1e6) return (nanos / 1e3).toFixed(1) + 'µs';
    if (nanos < 1e9) return (nanos / 1e6).toFixed(1) + 'ms';
    return (nanos / 1e9).toFixed(2) + 's';
  }

  function escapeHTML(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // Conservative subset of CSS.escape; only the chars that can
  // appear in our run IDs need handling.
  function cssEscape(s) {
    return String(s).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
  }
})();
