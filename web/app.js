'use strict';
const $ = (s) => document.querySelector(s);
const results = $('#results');
const status = $('#status');
const query = $('#query');
const modelFilter = $('#model-filter');
const brandFilter = $('#brand-filter');
const regionFilter = $('#region-filter');
const revisionFilter = $('#revision-filter');
const identifierFilter = $('#identifier-filter');
const verifiedFilter = $('#verified-filter');
const advanced = $('#model-advanced');
let selectedTab = 'models';
let allModels = [];

function node(tag, css, text) {
  const el = document.createElement(tag);
  if (css) el.className = css;
  if (text !== undefined) el.textContent = text;
  return el;
}
function label(text) { return node('span', 'chip', text); }
function empty(text) { const card = node('div', 'empty', text); results.append(card); }

async function request(path) {
  const response = await fetch(path, { headers: { 'Accept': 'application/json' } });
  if (!response.ok) throw new Error('Ошибка загрузки (' + response.status + ')');
  return response.json();
}

function modelCard(item) {
  const card = node('article', 'item-card');
  const top = node('div', 'card-top');
  top.append(label(item.brand));
  if (item.device_id) top.append(label(item.device_id + ' (без проверки ревизии)'));
  top.append(label('Вариантов: ' + item.variant_count));
  if (item.verified_variant_count) top.append(label('С источником: ' + item.verified_variant_count));
  card.append(top, node('h3', '', item.name), node('p', 'muted', item.description || 'Информация о модели'));
  const footer = node('div', 'card-bottom');
  footer.append(node('span', 'mono', item.slug));
  const select = node('button', 'text-button', 'Материалы →');
  select.addEventListener('click', () => {
    modelFilter.value = item.slug;
    setTab('voicepacks');
    document.querySelector('.workarea').scrollIntoView({ behavior: 'smooth' });
  });
  const variants = node('button', 'text-button', 'Аппаратные ревизии');
  variants.addEventListener('click', async () => {
    const existing = card.querySelector('.variant-list');
    if (existing) { existing.remove(); return; }
    const list = node('div','variant-list','Загрузка…');
    card.append(list);
    try {
      const items = await request('/api/v1/models/' + encodeURIComponent(item.slug) + '/variants');
      list.replaceChildren();
      if (!items.length) list.append(node('p','muted','Ревизии ещё не добавлены. Совместимость не установлена.'));
      for (const v of items) {
        const part = node('div','variant-card');
        part.append(node('strong','',v.sku || v.id),node('span','mono',(v.region || 'регион не указан') + ' · ' + (v.hardware_revision || 'ревизия неизвестна')));
        part.append(node('p','muted',v.verified ? 'Есть идентификатор с источником (не гарантия совместимости прошивки)' : 'Аппаратный идентификатор не подтверждён'));
        if (v.notes) part.append(node('p','muted',v.notes));
        for (const id of v.identifiers) {
          const line = node('p','mono',id.kind + ': ' + id.value + ' · ' + (id.verification==='verified' ? 'с источником' : 'не подтверждено'));
          if (id.source_url) {
            const a = node('a','',' Источник'); a.href=id.source_url; a.target='_blank'; a.rel='noopener noreferrer nofollow'; line.append(a);
          }
          part.append(line);
        }
        list.append(part);
      }
    } catch (error) { list.textContent = error.message; }
  });
  footer.append(variants, select); card.append(footer); return card;
}
function assetCard(item) {
  const card = node('article', 'item-card');
  const top = node('div', 'card-top');
  const model = allModels.find((m) => m.slug === item.model_slug);
  top.append(label(model ? model.name : item.model_slug));
  if (item.language) top.append(label(item.language));
  if (item.region) top.append(label('Регион: ' + item.region));
  if (item.kind === 'firmware') top.append(label('Совместимость не подтверждена'));
  card.append(top, node('h3', '', item.title));
  const details = item.version ? 'Версия: ' + item.version : 'Голосовой пакет';
  card.append(node('p', 'muted', details + (item.notes ? ' · ' + item.notes : '')));
  const footer = node('div', 'card-bottom');
  footer.append(node('span', 'mono', (item.size_bytes / (1024 * 1024)).toFixed(1) + ' МБ · SHA-256: ' + item.sha256.slice(0, 12) + '…'));
  const download = node('a', 'download', 'Скачать ↓');
  download.href = '/api/v1/assets/' + encodeURIComponent(item.id) + '/download';
  download.rel = 'nofollow';
  footer.append(download); card.append(footer); return card;
}

async function render() {
  results.replaceChildren(); status.textContent = 'Загрузка…';
  try {
    const q = query.value.trim();
    const params = new URLSearchParams();
    if (q) params.set('q', q);
    if (selectedTab !== 'models' && modelFilter.value) params.set('model', modelFilter.value);
    if (selectedTab === 'models') {
      if (brandFilter.value) params.set('brand',brandFilter.value);
      if (regionFilter.value.trim()) params.set('region',regionFilter.value.trim());
      if (revisionFilter.value.trim()) params.set('revision',revisionFilter.value.trim());
      if (identifierFilter.value.trim()) params.set('identifier',identifierFilter.value.trim());
      if (verifiedFilter.value) params.set('verified',verifiedFilter.value);
    }
    const items = await request('/api/v1/' + selectedTab + '?' + params.toString());
    status.textContent = items.length ? 'Найдено: ' + items.length : '';
    if (!items.length) { empty('Пока ничего не найдено. Попробуйте изменить фильтры.'); return; }
    const fragment = document.createDocumentFragment();
    for (const item of items) fragment.append(selectedTab === 'models' ? modelCard(item) : assetCard(item));
    results.append(fragment);
  } catch (error) { status.textContent = error.message; }
}
function setTab(tab) {
  selectedTab = tab;
  document.querySelectorAll('.tab').forEach((el) => {
    const active = el.dataset.tab === tab;
    el.classList.toggle('active', active);
    el.setAttribute('aria-selected', String(active));
  });
  modelFilter.disabled = tab === 'models';
  advanced.hidden = tab !== 'models';
  query.placeholder = tab === 'models' ? 'Название, производитель или код модели' : 'Поиск по названию файла';
  render();
}
async function init() {
  try {
    allModels = await request('/api/v1/models');
    const manufacturers = await request('/api/v1/manufacturers');
    for (const m of manufacturers) {
      const option = node('option', '', m.name);
      option.value = m.name; brandFilter.append(option);
    }
    const brands = allModels.sort((a,b) => a.brand.localeCompare(b.brand) || a.name.localeCompare(b.name));
    for (const m of brands) {
      const option = node('option', '', m.brand + ' · ' + m.name);
      option.value = m.slug;
      modelFilter.append(option);
    }
  } catch (_) { /* The main catalog error is displayed by render(). */ }
  await render();
}
let debounce;
query.addEventListener('input', () => { clearTimeout(debounce); debounce = setTimeout(render, 220); });
modelFilter.addEventListener('change', render);
[brandFilter,verifiedFilter].forEach((el) => el.addEventListener('change', render));
[regionFilter,revisionFilter,identifierFilter].forEach((el) => el.addEventListener('input', () => {
  clearTimeout(debounce); debounce = setTimeout(render, 250);
}));
document.querySelectorAll('.tab').forEach((tab) => tab.addEventListener('click', () => setTab(tab.dataset.tab)));
init();
