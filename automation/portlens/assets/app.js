/* Port lens client. Vanilla JS, no framework, no network. Every data file is a script that registers itself, so the report works from file://. */
(function () {
  'use strict';
  var PL = (window.PL = { ledgers: {}, frags: {}, sn: {}, shards: {}, waiting: {} });
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };
  var esc = function (s) { return String(s).replace(/[&<>"']/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]; }); };
  var fmt = function (n) { return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ','); };

  /* ---------- script loading ---------- */
  var loaded = {};
  function loadScript(src) {
    if (loaded[src]) return loaded[src];
    loaded[src] = new Promise(function (res, rej) {
      var s = document.createElement('script');
      s.src = src; s.async = true;
      s.onload = function () { res(); };
      s.onerror = function () { rej(new Error('could not load ' + src)); };
      document.head.appendChild(s);
    });
    return loaded[src];
  }
  PL.ledger = function (o) { PL.ledgers[o.id] = o.data; };
  PL.snips = function (o) { for (var k in o) PL.sn[k] = o[k]; };
  PL.frag = function (name, html) { PL.frags[name] = html; };

  /* ---------- theme ---------- */
  var themeBtn = $('#theme');
  function themeLabel(t) { return t === 'dark' ? 'Dark' : t === 'light' ? 'Light' : 'Auto'; }
  function applyTheme(t) {
    if (t === 'auto') document.documentElement.removeAttribute('data-theme'); else document.documentElement.setAttribute('data-theme', t);
    if (themeBtn) { themeBtn.textContent = 'Theme: ' + themeLabel(t); themeBtn.setAttribute('aria-label', 'Theme: ' + themeLabel(t) + '. Activate to change.'); }
  }
  var theme = 'auto';
  try { theme = localStorage.getItem('pl-theme') || 'auto'; } catch (e) { /* storage may be blocked on file:// */ }
  applyTheme(theme);
  if (themeBtn) themeBtn.addEventListener('click', function () {
    theme = theme === 'auto' ? 'dark' : theme === 'dark' ? 'light' : 'auto';
    try { localStorage.setItem('pl-theme', theme); } catch (e) { /* ignore */ }
    applyTheme(theme);
  });

  /* ---------- router ---------- */
  var VIEWS = ['overview', 'portmap', 'divergences', 'bugs', 'evals'];
  var current = null;
  function parseHash() {
    var h = location.hash || '#/overview';
    var m;
    if ((m = /^#(D\d+)$/.exec(h))) return { view: 'divergences', anchor: m[1] };
    if ((m = /^#(BF-[A-Za-z0-9-]+)$/.exec(h))) return { view: 'bugs', anchor: m[1] };
    var rest = h.replace(/^#\/?/, ''), q = '';
    var i = rest.indexOf('?');
    if (i >= 0) { q = rest.slice(i + 1); rest = rest.slice(0, i); }
    if (VIEWS.indexOf(rest) < 0) rest = 'overview';
    return { view: rest, query: q };
  }
  function show(route) {
    VIEWS.forEach(function (v) {
      var sec = $('#view-' + v);
      if (sec) sec.hidden = v !== route.view;
    });
    $$('.nav a').forEach(function (a) {
      if (a.dataset.view === route.view) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    });
    var titles = { overview: 'Overview', portmap: 'Port map', divergences: 'Divergences', bugs: 'Bug ledger', evals: 'Evals and benchmarks' };
    document.title = titles[route.view] + ' · PiG port lens';
    current = route.view;
    if (route.view === 'portmap') initPortmap(route.query || '');
    if (route.view === 'divergences') initFrag('divergences', route.anchor);
    if (route.view === 'bugs') initFrag('bugs', route.anchor);
    if (route.view === 'evals') initFrag('evals');
    if (route.view !== 'overview' || !route.anchor) window.scrollTo(0, 0);
  }
  function route() { show(parseHash()); }
  window.addEventListener('hashchange', route);

  /* ---------- fragments (divergences, bugs, evals) ---------- */
  var fragInit = {};
  function initFrag(name, anchor) {
    var host = $('#frag-' + name);
    var ready = function () {
      if (!fragInit[name]) {
        host.innerHTML = PL.frags[name];
        fragInit[name] = true;
        wireFilters(host);
      }
      if (anchor) {
        var el = document.getElementById(anchor);
        if (el) {
          if (el.tagName === 'DETAILS') el.open = true;
          el.scrollIntoView();
          var s = el.querySelector('summary'); if (s) s.focus({ preventScroll: true });
        }
      }
    };
    if (PL.frags[name]) return ready();
    loadScript('views/' + name + '.js').then(ready, function (e) { host.textContent = String(e.message); });
  }
  function wireFilters(root) {
    $$('[data-filter]', root).forEach(function (box) {
      var list = $('#' + box.dataset.filter, root);
      var items = $$('[data-search]', list);
      var input = $('input[type=search]', box);
      var count = $('.count', box);
      var groups = {};
      $$('button.toggle', box).forEach(function (b) {
        b.addEventListener('click', function () { b.setAttribute('aria-pressed', b.getAttribute('aria-pressed') === 'true' ? 'false' : 'true'); apply(); });
      });
      if (input) input.addEventListener('input', apply);
      var sels = $$('select[data-attr]', box);
      sels.forEach(function (s) { s.addEventListener('change', apply); });
      function apply() {
        var q = input ? input.value.trim().toLowerCase().split(/\s+/).filter(Boolean) : [];
        groups = {};
        $$('button.toggle[aria-pressed=true]', box).forEach(function (b) { (groups[b.dataset.attr] = groups[b.dataset.attr] || []).push(b.dataset.value); });
        var n = 0;
        items.forEach(function (it) {
          var ok = q.every(function (t) { return it.dataset.search.indexOf(t) >= 0; });
          if (ok) for (var a in groups) { if (groups[a].indexOf(it.dataset[a]) < 0) { ok = false; break; } }
          if (ok) sels.forEach(function (s) { if (s.value && it.dataset[s.dataset.attr] !== s.value) ok = false; });
          it.hidden = !ok;
          if (ok) n++;
        });
        if (count) count.textContent = n + ' of ' + items.length;
      }
      apply();
    });
  }

  /* ---------- port map ---------- */
  var STATUS = ['ported', 'derived', 'designed-out', 'gap'];
  var CLASS_NAME = { D: 'Designed out', W: 'Wire', R: 'Registration', U: 'Union', F: 'Behaviour', N: 'None (data shape)' };
  var CLASS_HELP = {
    D: 'Designed out: needs a recorded reason (a D-number or a probe) and the design-out audit columns.',
    W: 'Wire: needs a per-SDK conformance row or a wiring test that names the member.',
    R: 'Registration: needs a field-survival test that names the member.',
    U: 'Union: needs an exhaustive-handling test that names the union.',
    F: 'Behaviour: needs an asserting Go test that is mutation-checked or runs the Pi oracle.',
    N: 'None: a data shape has no behaviour to prove.'
  };
  var PAGE = 100;
  var st = null; // port map state
  var pmReady = false;

  function ledgerKeys() { return $$('#pm-pin option').map(function (o) { return o.value; }); }

  function initPortmap(query) {
    var p = new URLSearchParams(query);
    var pin = p.get('pin') || $('#pm-pin').value || ledgerKeys()[0];
    if (!pmReady) { wirePortmap(); pmReady = true; }
    if (!st || st.pin !== pin || (st.query !== query && query)) {
      st = { pin: pin, L: null, status: [1, 1, 1, 1], cls: p.get('cls') || '', fam: p.get('fam') || '', proof: p.get('proof') || '', pkg: p.get('pkg') || '', file: p.get('file') || '', q: p.get('q') || '', page: parseInt(p.get('page') || '0', 10) || 0, sel: [], query: query };
      if (p.get('st')) st.status = p.get('st').split('').map(function (c) { return c === '1' ? 1 : 0; });
      $('#pm-pin').value = pin;
      $('#pm-q').value = st.q;
      $('#pm-cls').value = st.cls;
      $('#pm-proof').value = st.proof;
      $$('#pm-status button').forEach(function (b, i) { b.setAttribute('aria-pressed', st.status[i] ? 'true' : 'false'); });
      var host = $('#pm-list');
      host.innerHTML = '<li class="loading">Loading the ledger…</li>';
      loadScript('data/ledger-' + pin + '.js').then(function () {
        st.L = prepLedger(PL.ledgers[pin]);
        fillFamilies();
        $('#pm-fam').value = st.fam;
        $('#pm-pkg').innerHTML = '<option value="">All packages</option>' + st.L.pkgs.map(function (n, i) { return '<option value="' + i + '"' + (String(i) === st.pkg ? ' selected' : '') + '>' + esc(n) + '</option>'; }).join('');
        buildTree();
        refilter();
      }, function (e) { host.innerHTML = '<li class="empty">' + esc(e.message) + '</li>'; });
    }
  }

  function prepLedger(d) {
    if (d.ready) return d;
    d.n = d.rows.length;
    d.postCache = {};
    d.pkgRange = d.pkgs.map(function () { return [d.n, 0]; });
    d.pkgCount = d.pkgs.map(function () { return [0, 0, 0, 0]; });
    d.files.forEach(function (f) {
      var r = d.pkgRange[f[0]];
      r[0] = Math.min(r[0], f[2]); r[1] = Math.max(r[1], f[3]);
      for (var k = 0; k < 4; k++) d.pkgCount[f[0]][k] += f[4 + k];
    });
    d.ready = true;
    return d;
  }

  function fillFamilies() {
    $('#pm-fam').innerHTML = '<option value="">All owner families</option><option value="none">(no family)</option>' + st.L.fams.map(function (n, i) { return '<option value="' + (i + 1) + '">' + esc(n) + '</option>'; }).join('');
  }

  function wirePortmap() {
    $('#pm-pin').addEventListener('change', function () { var q = new URLSearchParams(); q.set('pin', this.value); location.hash = '#/portmap?' + q.toString(); });
    $('#pm-q').addEventListener('input', debounce(function () { st.q = this.value; st.page = 0; refilter(); }.bind($('#pm-q')), 140));
    $$('#pm-status button').forEach(function (b, i) {
      b.addEventListener('click', function () { st.status[i] = st.status[i] ? 0 : 1; b.setAttribute('aria-pressed', st.status[i] ? 'true' : 'false'); st.page = 0; refilter(); });
    });
    $('#pm-cls').addEventListener('change', function () { st.cls = this.value; st.page = 0; refilter(); });
    $('#pm-proof').addEventListener('change', function () { st.proof = this.value; st.page = 0; refilter(); });
    $('#pm-fam').addEventListener('change', function () { st.fam = this.value; st.page = 0; refilter(); });
    $('#pm-pkg').addEventListener('change', function () { st.pkg = this.value; st.file = ''; st.page = 0; syncTree(); refilter(); });
    $('#pm-prev').addEventListener('click', function () { if (st.page > 0) { st.page--; render(); window.scrollTo(0, $('#pm-top').offsetTop - 80); } });
    $('#pm-next').addEventListener('click', function () { if ((st.page + 1) * PAGE < st.sel.length) { st.page++; render(); window.scrollTo(0, $('#pm-top').offsetTop - 80); } });
    $('#pm-list').addEventListener('click', function (e) {
      var b = e.target.closest('.row-head');
      if (b) toggleRow(b);
    });
    $('#pm-tree').addEventListener('click', function (e) {
      var b = e.target.closest('button[data-pkg]');
      if (!b) return;
      if (b.dataset.file) { st.pkg = b.dataset.pkg; st.file = b.dataset.file; }
      else if (b.dataset.pkg === '') { st.pkg = ''; st.file = ''; }
      else if (st.pkg === b.dataset.pkg && !st.file) { st.pkg = ''; }
      else { st.pkg = b.dataset.pkg; st.file = ''; }
      st.page = 0; $('#pm-pkg').value = st.pkg; syncTree(); refilter();
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === '/' && !/^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement.tagName)) {
        var inp = current === 'portmap' ? $('#pm-q') : $('#view-' + current + ' input[type=search]');
        if (inp) { e.preventDefault(); inp.focus(); inp.select(); }
      } else if ((e.key === 'j' || e.key === 'k') && current === 'portmap' && !/^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement.tagName)) {
        var heads = $$('#pm-list .row-head'), cur = heads.indexOf(document.activeElement);
        var next = heads[Math.max(0, Math.min(heads.length - 1, cur + (e.key === 'j' ? 1 : -1)))];
        if (next) { e.preventDefault(); next.focus(); }
      }
    });
  }
  function debounce(fn, ms) { var t; return function () { var a = arguments, c = this; clearTimeout(t); t = setTimeout(function () { fn.apply(c, a); }, ms); }; }

  function buildTree() {
    var L = st.L, out = ['<ul><li><button type="button" data-pkg="" id="tree-all"><span class="tn">All upstream packages</span><span class="tc">' + fmt(L.n) + '</span></button></li>'];
    L.pkgs.forEach(function (name, i) {
      var c = L.pkgCount[i], tot = c[0] + c[1] + c[2] + c[3];
      out.push('<li><button type="button" data-pkg="' + i + '" aria-expanded="false"><span class="tn">' + esc(name) + '</span><span class="mini" title="' + miniTitle(c) + '">' + miniBar(c, tot) + '</span><span class="tc">' + fmt(tot) + '</span></button><ul hidden></ul></li>');
    });
    out.push('</ul>');
    $('#pm-tree').innerHTML = out.join('');
    syncTree();
  }
  function miniTitle(c) { return STATUS.map(function (s, k) { return c[k] + ' ' + s; }).join(', '); }
  function miniBar(c, tot) {
    var cols = ['ok', 'info', 'violet', 'bad'];
    return c.map(function (n, k) { return n ? '<i style="width:' + (100 * n / tot).toFixed(2) + '%;background:var(--' + cols[k] + ')"></i>' : ''; }).join('');
  }
  function syncTree() {
    var L = st.L;
    $$('#pm-tree > ul > li').forEach(function (li, idx) {
      if (idx === 0) return;
      var pkg = String(idx - 1), btn = li.firstElementChild, ul = li.lastElementChild;
      var open = st.pkg === pkg;
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
      btn.setAttribute('aria-current', open && !st.file ? 'true' : 'false');
      ul.hidden = !open;
      if (open && !ul.firstChild) {
        ul.innerHTML = L.files.map(function (f, fi) { return f[0] === idx - 1 ? { f: f, fi: fi } : null; }).filter(Boolean).map(function (x) {
          var f = x.f, c = [f[4], f[5], f[6], f[7]], own = 'packages/' + L.pkgs[f[0]] + '/src/', short = f[1].indexOf(own) === 0 ? f[1].slice(own.length) : f[1].replace(/^packages\//, '../');
          return '<li><button type="button" data-pkg="' + (idx - 1) + '" data-file="' + x.fi + '" title="' + esc(f[1]) + '"><span class="tn">' + esc(short) + '</span><span class="mini" title="' + miniTitle(c) + '">' + miniBar(c, f[3] - f[2]) + '</span><span class="tc">' + (f[3] - f[2]) + '</span></button></li>';
        }).join('');
      }
      if (open) $$('button[data-file]', ul).forEach(function (b) { b.setAttribute('aria-current', b.dataset.file === st.file ? 'true' : 'false'); });
    });
    $('#tree-all').setAttribute('aria-current', st.pkg === '' ? 'true' : 'false');
  }

  /* search index */
  function lowerBound(arr, x) { var lo = 0, hi = arr.length; while (lo < hi) { var m = (lo + hi) >> 1; if (arr[m] < x) lo = m + 1; else hi = m; } return lo; }
  function termMark(L, term) {
    var mark = new Uint8Array(L.n);
    if (/[:#/.]/.test(term)) {
      for (var i = 0; i < L.n; i++) { var r = L.rows[i]; if (r[0].toLowerCase().indexOf(term) >= 0 || (r[7].length && (L.str[r[7][0]] + '#' + L.str[r[7][2]]).toLowerCase().indexOf(term) >= 0)) mark[i] = 1; }
      return mark;
    }
    var lo = lowerBound(L.tok, term), cnt = 0;
    for (var t = lo; t < L.tok.length && L.tok[t].lastIndexOf(term, 0) === 0 && cnt < 3000; t++, cnt++) {
      var acc = 0, ds = L.post[t];
      for (var j = 0; j < ds.length; j++) { acc += ds[j]; mark[acc] = 1; }
    }
    return mark;
  }

  function refilter() {
    var L = st.L; if (!L) return;
    var a = 0, b = L.n;
    if (st.file !== '') { var f = L.files[+st.file]; a = f[2]; b = f[3]; }
    else if (st.pkg !== '') { var r = L.pkgRange[+st.pkg]; a = r[0]; b = r[1]; }
    var terms = st.q.toLowerCase().split(/\s+/).filter(Boolean);
    var marks = terms.map(function (t) { return termMark(L, t); });
    var sel = [];
    var fam = st.fam === 'none' ? 0 : st.fam === '' ? -1 : +st.fam;
    for (var i = a; i < b; i++) {
      var row = L.rows[i];
      if (!st.status[row[1]]) continue;
      if (st.cls && row[2] !== st.cls) continue;
      if (fam >= 0 && row[5] !== fam) continue;
      if (st.proof) {
        if (st.proof === 'proven' && row[17] !== 1) continue;
        if (st.proof === 'unproven' && row[17] !== 2) continue;
        if (st.proof === 'unknown' && row[17] !== 0) continue;
      }
      var ok = true;
      for (var m = 0; m < marks.length; m++) if (!marks[m][i]) { ok = false; break; }
      if (ok) sel.push(i);
    }
    st.sel = sel;
    if (st.page * PAGE >= sel.length) st.page = 0;
    render();
    writeState();
  }

  function writeState() {
    var q = new URLSearchParams();
    q.set('pin', st.pin);
    if (st.q) q.set('q', st.q);
    if (st.pkg !== '') q.set('pkg', st.pkg);
    if (st.file !== '') q.set('file', st.file);
    if (st.cls) q.set('cls', st.cls);
    if (st.fam) q.set('fam', st.fam);
    if (st.proof) q.set('proof', st.proof);
    if (st.status.join('') !== '1111') q.set('st', st.status.join(''));
    if (st.page) q.set('page', st.page);
    var h = '#/portmap?' + q.toString();
    st.query = q.toString();
    if (location.hash !== h) history.replaceState(null, '', h);
  }

  function shortName(id) {
    var i = id.indexOf('#');
    if (i < 0) { var j = id.indexOf('/'); return [id.slice(j + 1), '']; }
    var rest = id.slice(i + 1), k = rest.indexOf('::');
    return k < 0 ? [rest, ''] : [rest.slice(0, k), rest.slice(k)];
  }

  function render() {
    var L = st.L, sel = st.sel, from = st.page * PAGE, to = Math.min(sel.length, from + PAGE);
    var html = [];
    for (var k = from; k < to; k++) {
      var i = sel[k], r = L.rows[i], nm = shortName(r[0]), t = r[7];
      var goref = t.length ? L.str[t[0]] + (t[1] ? ':' + t[1] : '') : '';
      var tests = r[9].length / 3;
      html.push('<li class="row" data-i="' + i + '"><button type="button" class="row-head" aria-expanded="false" aria-controls="rb-' + i + '">' +
        '<span class="chip st-' + STATUS[r[1]] + '">' + STATUS[r[1]] + '</span>' +
        '<span class="rname" title="' + esc(r[0]) + '">' + esc(nm[0]) + '<small>' + esc(nm[1]) + '</small></span>' +
        '<span class="cls" title="' + esc(CLASS_NAME[r[2]]) + '">' + r[2] + '</span>' +
        '<span class="goref" title="' + esc(goref) + '"><bdi>' + esc(goref || 'no Go target') + '</bdi></span>' +
        '<span class="ntests">' + (tests ? tests + (tests === 1 ? ' test' : ' tests') : 'no test') + '</span></button></li>');
    }
    $('#pm-list').innerHTML = html.length ? html.join('') : '<li class="empty">No row matches these filters.</li>';
    $('#pm-count').textContent = sel.length ? 'Rows ' + fmt(from + 1) + '–' + fmt(to) + ' of ' + fmt(sel.length) + ' (' + fmt(L.n) + ' in this ledger)' : '0 of ' + fmt(L.n) + ' rows';
    $('#pm-prev').disabled = st.page === 0;
    $('#pm-next').disabled = to >= sel.length;
    $('#pm-page').textContent = sel.length ? 'Page ' + (st.page + 1) + ' of ' + Math.ceil(sel.length / PAGE) : '';
  }

  function refLink(L, f, line, sym) { return '<span class="mono">' + esc(f) + (line ? ':' + line : '') + (sym ? '<span class="muted"> ' + esc(sym) + '</span>' : '') + '</span>'; }

  function expandRationale(L, r) {
    var txt = L.rat[r[6]] || '';
    var nm = shortName(r[0])[0] + shortName(r[0])[1];
    return txt.replace(/\u0001(\d+)\u0002/g, function (_, n) { var t = r[7]; return t[n * 3] !== undefined ? L.str[t[n * 3]] + '#' + L.str[t[n * 3 + 2]] : ''; }).replace(/\u0003/g, nm);
  }

  function toggleRow(btn) {
    var li = btn.parentNode, i = +li.dataset.i, open = btn.getAttribute('aria-expanded') === 'true';
    var body = li.querySelector('.row-body');
    if (open) { btn.setAttribute('aria-expanded', 'false'); if (body) body.hidden = true; return; }
    btn.setAttribute('aria-expanded', 'true');
    if (!body) { body = document.createElement('div'); body.className = 'row-body'; body.id = 'rb-' + i; body.innerHTML = rowBody(i); li.appendChild(body); fillSnippets(i, body); }
    body.hidden = false;
  }

  function rowBody(i) {
    var L = st.L, r = L.rows[i], t = r[7], h = [];
    var file = L.files[r[3]];
    h.push('<div><h4>Upstream (Pi ' + esc(L.version) + ')</h4><p class="mono small">' + esc(file[1]) + (r[4] ? ':' + r[4] : '') + (r[20] >= 0 ? '<br><span class="muted">declared in ' + esc(L.str[r[20]]) + '</span>' : '') + '</p>' +
      '<div class="code" id="pi-' + i + '"><div class="cap"><span>' + (r[19] >= 0 ? esc(L.str[r[19]]) : 'declaration') + ' (from the published declaration inventory)</span></div>' + (r[16] ? '<div class="un">Loading…</div>' : '<div class="un">No declaration text in the inventory.</div>') + '</div></div>');
    h.push('<div><h4>Go (PiG)</h4>' + (t.length ? '<ul class="reflist">' + groups3(t, L).map(function (x) { return '<li>' + refLink(L, x[0], x[1], x[2]) + '</li>'; }).join('') + '</ul>' : '<p>No Go target is recorded for this row.</p>') +
      (t.length && t[1] ? '<div class="code" id="go-' + i + '"><div class="cap"><span>' + esc(L.str[t[0]]) + '</span></div><div class="un">Loading…</div></div>' : '') + '</div>');
    var meta = [];
    meta.push('<h4>Mapping</h4><p>' + esc(expandRationale(L, r)) + '</p>');
    if (r[14] >= 0) meta.push('<p><span class="chip bad">gap</span> ' + esc(L.str[r[14]]) + '</p>');
    if (r[11] >= 0) meta.push('<p class="mono small">layers: ' + esc(L.str[r[11]]) + '</p>');
    if (r[12] >= 0) meta.push('<p><b>Async contract.</b> ' + esc(L.str[r[12]]) + '</p>');
    if (r[13].length) meta.push('<p class="mono small">behavior contracts: ' + r[13].map(function (x) { return esc(L.str[x]); }).join(', ') + '</p>');
    if (r[15].length) meta.push('<p>Divergences: ' + r[15].map(function (d) { return '<a class="chip neutral" href="#' + d + '">' + d + '</a>'; }).join(' ') + '</p>');
    h.push('<div class="full">' + meta.join('') + '</div>');
    var proof = '<h4>Proof class ' + r[2] + ': ' + esc(CLASS_NAME[r[2]]) + '</h4><p>' + esc(CLASS_HELP[r[2]]) + '</p>';
    if (r[1] !== 3) {
      if (r[17] === 1) proof += '<p><span class="chip ok">proven</span> The proof-class tool does not list this row as unproven.</p>';
      else if (r[17] === 2) proof += '<p><span class="chip bad">unproven</span> ' + esc(r[18] >= 0 ? L.str[r[18]] : '') + '</p>';
      else proof += '<p class="muted">Proof status is read from <code>build/ledger-proof-classes/unproven-behaviour.tsv</code> (<code>make ledger-check</code>); that run was not present when this report was built.</p>';
    }
    h.push('<div>' + proof + '</div>');
    var prod = r[8], p = [];
    for (var k = 0; k < prod.length; k += 4) p.push('<li><span class="k">' + esc(L.str[prod[k]]) + '</span>' + refLink(L, L.str[prod[k + 1]], prod[k + 2], L.str[prod[k + 3]]) + '</li>');
    h.push('<div><h4>Production path (' + p.length + ')</h4>' + (p.length ? '<ul class="reflist">' + p.slice(0, 12).join('') + (p.length > 12 ? '<li class="muted">… ' + (p.length - 12) + ' more</li>' : '') + '</ul>' : '<p class="muted">None recorded.</p>') + '</div>');
    var tt = groups3(r[9], L), tl = tt.map(function (x) { return '<li>' + refLink(L, x[0], x[1], x[2]) + '</li>'; });
    h.push('<div class="full"><h4>Tests that prove it (' + tl.length + ')</h4>' + (tl.length ? '<ul class="reflist">' + tl.slice(0, 40).join('') + (tl.length > 40 ? '<li class="muted">… ' + (tl.length - 40) + ' more</li>' : '') + '</ul>' : '<p class="muted">No test is recorded as evidence for this row.</p>') + '</div>');
    return h.join('');
  }
  function groups3(a, L) { var o = []; for (var k = 0; k < a.length; k += 3) o.push([L.str[a[k]], a[k + 1], L.str[a[k + 2]]]); return o; }

  function fnv(key) {
    var b = new TextEncoder().encode(key), h = 0x811c9dc5;
    for (var i = 0; i < b.length; i++) { h ^= b[i]; h = Math.imul(h, 0x01000193) >>> 0; }
    return h;
  }
  function snippet(key) {
    if (PL.sn[key] !== undefined) return Promise.resolve(PL.sn[key]);
    var n = fnv(key) % 32, name = 'data/snip-' + (n < 10 ? '0' : '') + n + '.js';
    return loadScript(name).then(function () { return PL.sn[key] || null; });
  }
  function codeInto(el, caption, raw) {
    if (!raw) { el.innerHTML = '<div class="cap"><span>' + esc(caption) + '</span></div><div class="un">No snippet available.</div>'; return; }
    var nl = raw.indexOf('\n'), range = raw.slice(0, nl), body = raw.slice(nl + 1);
    el.innerHTML = '<div class="cap"><span>' + esc(caption) + (range ? ':' + esc(range) : '') + '</span></div><pre tabindex="0"' + (range ? '' : ' class="wrap"') + '><code>' + body + '</code></pre>';
  }
  function fillSnippets(i, body) {
    var L = st.L, r = L.rows[i], t = r[7];
    if (r[16]) snippet('pi:' + st.pin + ':' + r[0]).then(function (raw) { codeInto($('#pi-' + i, body), (r[19] >= 0 ? L.str[r[19]] : 'declaration') + ' (published declaration)', raw); }, function () { });
    if (t.length && t[1]) snippet(L.str[t[0]] + ':' + t[1]).then(function (raw) { codeInto($('#go-' + i, body), L.str[t[0]], raw); }, function () { });
  }

  /* ---------- start ---------- */
  document.addEventListener('DOMContentLoaded', route);
  if (document.readyState !== 'loading') route();
})();
