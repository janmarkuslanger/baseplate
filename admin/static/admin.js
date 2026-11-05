// baseplate admin: Alpine components for the content editor.
(function () {
  'use strict';

  const csrf = () => document.body.dataset.csrf || '';
  // New items get "n…" keys; stored items keep the server's "<table>:<id>".
  const uid = () => 'n' + Math.random().toString(36).slice(2, 10);

  // _key identifies items: x-for uses it, and the server reuses stored
  // components by it, so blocks keep their IDs across saves and moves.
  function addKeys(v) {
    if (Array.isArray(v)) {
      v.forEach((it) => {
        if (it && typeof it === 'object' && !Array.isArray(it)) {
          if (!it._key) it._key = uid();
          Object.values(it).forEach(addKeys);
        }
      });
    } else if (v && typeof v === 'object') {
      Object.values(v).forEach(addKeys);
    }
    return v;
  }
  const stripKeys = (k, v) => (k === '_key' ? undefined : v);
  // clone deep-copies an item and gives it (and its nested items) fresh keys.
  const clone = (v) => addKeys([JSON.parse(JSON.stringify(v, stripKeys))])[0];

  window.withKey = clone;

  // containsList reports whether list is item itself or nested inside it.
  function containsList(item, list) {
    const target = Alpine.raw(list);
    const walk = (v) => {
      v = Alpine.raw(v);
      if (v === target) return true;
      if (Array.isArray(v)) return v.some(walk);
      if (v && typeof v === 'object') return Object.values(v).some(walk);
      return false;
    };
    return walk(item);
  }

  window.slugify = function (s, keepTrailing) {
    s = String(s).toLowerCase()
      .replace(/ä/g, 'ae').replace(/ö/g, 'oe').replace(/ü/g, 'ue').replace(/ß/g, 'ss')
      .normalize('NFKD').replace(/[̀-ͯ]/g, '')
      .replace(/[^a-z0-9]+/g, '-').replace(/^-+/, '');
    return keepTrailing ? s : s.replace(/-+$/, '');
  };

  document.addEventListener('alpine:init', () => {
    Alpine.data('editor', () => ({
      data: {},
      errors: {},
      blocks: {},
      labels: {},
      tab: '',
      dirty: false,
      submitting: false,
      dragList: null,
      dragIndex: -1,

      init() {
        const ds = this.$el.dataset;
        this.data = addKeys(JSON.parse(ds.initial || '{}'));
        this.errors = JSON.parse(ds.errors || 'null') || {};
        this.blocks = JSON.parse(ds.blocks || '{}');
        this.labels = JSON.parse(ds.labels || '{}');
        this.$nextTick(() => {
          const firstErr = this.$root.querySelector('.tabs__tab.has-error');
          if (firstErr) firstErr.click();
          this.$watch('data', () => { this.dirty = true; });
        });
        window.addEventListener('beforeunload', (e) => {
          if (this.dirty && !this.submitting) e.preventDefault();
        });
        document.addEventListener('keydown', (e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 's') {
            e.preventDefault();
            this.$root.querySelector('.actions .btn--primary').click();
          }
        });
      },

      err(path) { return this.errors[path] || ''; },
      tabHasError(fields) {
        const names = fields.map((f) => f.Name);
        return Object.keys(this.errors).some((k) => names.includes(k.split('.')[0]));
      },
      serialize() {
        this.submitting = true;
        this.$refs.data.value = JSON.stringify(this.data);
      },

      newBlock(name) { return clone(this.blocks[name]); },
      blockLabel(name) { return this.labels[name] || name; },
      move(list, i, d) {
        const j = i + d;
        if (j < 0 || j >= list.length) return;
        const [it] = list.splice(i, 1);
        list.splice(j, 0, it);
      },
      duplicate(list, i) { list.splice(i + 1, 0, clone(list[i])); },

      // mountBlock inserts a block's editor once and initializes it. Only
      // the new children are initialized, so the host's own directives do
      // not run again; nothing here is reactive, so typing never re-mounts.
      mountBlock(el, name) {
        const tpl = document.getElementById('bp-block-' + name);
        if (!tpl) return;
        el.innerHTML = tpl.innerHTML;
        [...el.children].forEach((child) => Alpine.initTree(child));
      },

      // Drag & drop works within a list and across nesting levels.
      dragStart(e, list, i) {
        this.dragList = list;
        this.dragIndex = i;
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', String(i));
        const card = e.target.closest('.card');
        if (card) e.dataTransfer.setDragImage(card, 20, 20);
      },
      dragOver(e) { if (this.dragList) e.currentTarget.classList.add('is-drop-target'); },
      dragLeave(e) { e.currentTarget.classList.remove('is-drop-target'); },
      drop(e, list, i) {
        e.currentTarget.classList.remove('is-drop-target');
        const from = this.dragList;
        if (!from) return;
        const item = from[this.dragIndex];
        // An item cannot be dropped into itself or one of its descendants.
        if (!item || containsList(item, list)) { this.dragEnd(); return; }
        if (Alpine.raw(from) === Alpine.raw(list)) {
          if (this.dragIndex === i) { this.dragEnd(); return; }
          from.splice(this.dragIndex, 1);
          list.splice(i > this.dragIndex ? i - 1 : i, 0, item);
        } else {
          from.splice(this.dragIndex, 1);
          list.splice(i, 0, item);
        }
        this.dragEnd();
      },
      dragEnd() { this.dragList = null; this.dragIndex = -1; },
    }));

    Alpine.data('richtext', () => ({
      value: '',
      init() {
        const ed = this.$refs.editor;
        ed.innerHTML = this.value || '';
        this.$watch('value', (v) => { if (v !== ed.innerHTML) ed.innerHTML = v || ''; });
        ed.addEventListener('focus', () => document.execCommand('defaultParagraphSeparator', false, 'p'));
        ed.addEventListener('paste', (e) => {
          // Paste as plain text; formatting comes from the toolbar.
          e.preventDefault();
          document.execCommand('insertText', false, e.clipboardData.getData('text/plain'));
        });
      },
      sync() {
        const html = this.$refs.editor.innerHTML;
        this.value = html === '<br>' ? '' : html;
      },
      cmd(c) { this.$refs.editor.focus(); document.execCommand(c, false, null); this.sync(); },
      block(tag) { this.$refs.editor.focus(); document.execCommand('formatBlock', false, tag); this.sync(); },
      link() {
        const url = window.prompt('Link-Adresse (https://…, /pfad oder mailto:…)');
        if (!url) return;
        this.$refs.editor.focus();
        document.execCommand('createLink', false, url);
        this.sync();
      },
    }));

    // relationField edits one target ID (or a list of IDs when many).
    Alpine.data('relationField', (options, many) => ({
      value: many ? [] : null,
      options,
      many,
      q: '',
      open: false,
      label(id) {
        const o = this.options.find((o) => Number(o.Value) === Number(id));
        return o ? o.Label : '#' + id + ' (gelöscht?)';
      },
      matches() {
        const q = this.q.toLowerCase();
        const taken = this.many ? (this.value || []).map(Number) : [];
        return this.options
          .filter((o) => !taken.includes(Number(o.Value)) && o.Label.toLowerCase().includes(q))
          .slice(0, 50);
      },
      pick(o) {
        const id = Number(o.Value);
        if (this.many) this.value = [...(this.value || []), id];
        else this.value = id;
        this.q = '';
        this.open = this.many;
      },
      remove(i) { this.value = this.value.filter((_, j) => j !== i); },
      move(i, d) {
        const j = i + d;
        if (j < 0 || j >= this.value.length) return;
        const v = [...this.value];
        [v[i], v[j]] = [v[j], v[i]];
        this.value = v;
      },
    }));

    Alpine.data('imageField', () => ({
      value: '',
      busy: false,
      error: '',
      library: false,
      files: [],
      async upload(e) {
        const file = e.target.files[0];
        if (!file) return;
        this.busy = true;
        this.error = '';
        const body = new FormData();
        body.append('file', file);
        try {
          const res = await fetch('/admin/media', { method: 'POST', body, headers: { 'X-CSRF-Token': csrf(), Accept: 'application/json' } });
          const out = await res.json();
          if (!res.ok) throw new Error(out.error || 'Upload fehlgeschlagen');
          this.value = out.url;
        } catch (err) {
          this.error = err.message;
        } finally {
          this.busy = false;
          e.target.value = '';
        }
      },
      async openLibrary() {
        this.library = true;
        const res = await fetch('/admin/media.json');
        this.files = await res.json();
      },
    }));
  });
})();
