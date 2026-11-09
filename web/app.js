'use strict';
(() => {
  let token = '', generation = 0, controller = null, busy = false;
  const $ = id => document.getElementById(id);
  const node = (tag, text, className) => {
    const element = document.createElement(tag);
    if (text !== undefined) element.textContent = text;
    if (className) element.className = className;
    return element;
  };
  async function api(account, signal) {
    const url = new URL('./data', window.location.href);
    if (account !== undefined) url.searchParams.set('account', account);
    const response = await fetch(url, {headers: {'X-Ollama-Monitor-Token': token}, cache: 'no-store', credentials: 'omit', signal});
    const data = await response.json();
    if (!response.ok) {
      const error = new Error(data.error || 'Request failed');
      error.status = response.status;
      throw error;
    }
    return data;
  }
  function lock() {
    generation++; controller?.abort(); controller = null;
    token = ''; busy = false; $('token').value = '';
    $('cards').replaceChildren(); $('dashboard').hidden = true; $('login').hidden = false;
    $('refresh').disabled = false; $('status').textContent = '';
    document.querySelector('#unlock button').disabled = false;
  }
  function showQuota(card, data) {
    const q = data.quota;
    if (q.subscription) card.append(node('p', q.subscription.plan, 'muted'));
    for (const group of q.groups || []) for (const bucket of group.buckets || []) {
      const used = Math.max(0, Math.min(100, (1 - bucket.remainingFraction) * 100));
      const section = node('section', undefined, 'window');
      section.append(node('h3', bucket.description), node('strong', `${used.toFixed(1)}% used`));
      const progress = node('progress'); progress.max = 100; progress.value = used;
      progress.setAttribute('aria-label', bucket.description + ' used'); section.append(progress);
      if (bucket.resetTime) section.append(node('p', 'Resets ' + new Date(bucket.resetTime).toLocaleString(), 'muted'));
      card.append(section);
    }
    for (const metric of q.summary || []) {
      const value = metric.format === 'currency' ? new Intl.NumberFormat(undefined, {style: 'currency', currency: metric.currency || 'USD'}).format(metric.value) : String(metric.value);
      card.append(node('p', `${metric.label}: ${value}`));
    }
    card.append(node('p', 'Displayed ' + new Date(data.served_at).toLocaleTimeString() + ' · may be cached for 60 seconds', 'muted'));
  }
  async function refresh() {
    if (busy) return;
    busy = true;
    const current = ++generation;
    controller?.abort(); controller = new AbortController(); const signal = controller.signal;
    $('refresh').disabled = true; document.querySelector('#unlock button').disabled = true;
    $('status').textContent = 'Loading accounts…';
    try {
      const data = await api(undefined, signal);
      if (current !== generation) return;
      $('login').hidden = true; $('dashboard').hidden = false; $('cards').replaceChildren();
      $('status').textContent = data.accounts.length ? 'Refreshing quota…' : 'No accounts configured. Add accounts in the server’s plugin configuration.';
      let failures = 0;
      for (const name of data.accounts) {
        if (current !== generation) return;
        const card = node('article', undefined, 'panel'); card.append(node('h2', name)); $('cards').append(card);
        try {
          const quota = await api(name, signal);
          if (current !== generation) return;
          showQuota(card, quota);
        } catch (error) {
          if (current !== generation) return;
          if (error.status === 401 || error.status === 503) throw error;
          failures++; card.append(node('p', error.message, 'error'));
        }
      }
      if (data.accounts.length) $('status').textContent = failures ? `${failures} account(s) could not be refreshed.` : 'Quota updated.';
    } catch (error) {
      if (current !== generation) return;
      lock(); $('status').textContent = error.message;
    } finally {
      if (current === generation) {busy = false; $('refresh').disabled = false; document.querySelector('#unlock button').disabled = false;}
    }
  }
  $('unlock').addEventListener('submit', event => {event.preventDefault(); token = $('token').value.trim(); $('token').value = ''; refresh();});
  $('refresh').addEventListener('click', refresh);
  $('lock').addEventListener('click', lock);
  window.addEventListener('pagehide', lock);
})();
