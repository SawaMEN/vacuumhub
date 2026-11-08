'use strict';
const $ = (s) => document.querySelector(s);
const results = $('#results');
const status = $('#status');
const query = $('#query');
const modelFilter = $('#model-filter');
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
  if (item.device_id) top.append(label(item.device_id));
  card.append(top, node('h3', '', item.name), node('p', 'muted', item.description || 'Информация о модели'));
  const footer = node('div', 'card-bottom');
  footer.append(node('span', 'mono', item.slug));
  const select = node('button', 'text-button', 'Материалы →');
  select.addEventListener('click', () => {
    modelFilter.value = item.slug;
    setTab('voicepacks');
    document.querySelector('.workarea').scrollIntoView({ behavior: 'smooth' });
  });
  footer.append(select); card.append(footer); return card;
}
function assetCard(item) {
  const card = node('article', 'item-card');
  const top = node('div', 'card-top');
  const model = allModels.find((m) => m.slug === item.model_slug);
  top.append(label(model ? model.name : item.model_slug));
  if (item.language) top.append(label(item.language));
  if (item.region) top.append(label('Регион: ' + item.region));
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
  query.placeholder = tab === 'models' ? 'Название, производитель или код модели' : 'Поиск по названию файла';
  render();
}
async function init() {
  try {
    allModels = await request('/api/v1/models');
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
document.querySelectorAll('.tab').forEach((tab) => tab.addEventListener('click', () => setTab(tab.dataset.tab)));
init();
