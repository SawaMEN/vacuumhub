'use strict';
const token = document.getElementById('token');
const notice = document.getElementById('notice');
const pending = document.getElementById('pending');
const modelSelect = document.getElementById('upload-model');
const variantModelSelect = document.getElementById('variant-model');
const manufacturerSelect = document.getElementById('manufacturer-select');
function notify(message, good = false) { notice.textContent = message; notice.className = 'notice ' + (good ? 'good' : 'bad'); }
function element(tag, cls, text) { const n = document.createElement(tag); if(cls)n.className=cls; if(text!==undefined)n.textContent=text; return n; }
async function api(path, options = {}) {
  const headers = {Authorization:'Bearer '+token.value.trim()};
  if (!(options.body instanceof FormData) && options.body) headers['Content-Type'] = 'application/json';
  const r = await fetch('/api/v1/' + path, { ...options, headers });
  const payload = await r.json();
  if (!r.ok) throw new Error(payload.error || 'Ошибка HTTP ' + r.status);
  return payload;
}
async function refresh() {
  if (!token.value.trim()) throw new Error('Введите ключ администратора');
  const [models, assets, manufacturers] = await Promise.all([
    fetch('/api/v1/models').then((r) => r.json()),
    api('admin/assets?status=pending'),
    fetch('/api/v1/manufacturers').then((r) => r.json())
  ]);
  modelSelect.replaceChildren();variantModelSelect.replaceChildren();manufacturerSelect.replaceChildren();
  for (const model of models) {
    for (const select of [modelSelect,variantModelSelect]) {
      const opt=element('option','',model.brand+' · '+model.name);opt.value=model.slug;select.append(opt);
    }
  }
  for (const manufacturer of manufacturers) {
    const opt=element('option','',manufacturer.name);opt.value=manufacturer.name;manufacturerSelect.append(opt);
  }
  pending.replaceChildren();
  if (!assets.length) {pending.append(element('p','muted','Нет материалов, ожидающих модерации.'));return;}
  for (const asset of assets) {
    const card=element('div','review-card');
    card.append(element('strong','',asset.title),element('p','small muted',asset.kind+' · '+asset.model_slug+' · '+asset.original_filename),element('code','checksum',asset.sha256));
    if(asset.kind==='voicepack')card.append(element('p','small muted','Автор: '+(asset.author || 'не указан')+' · Лицензия: '+(asset.license || 'не указана')+' · Серия: '+(asset.series_slug || 'старый пакет')+' · Формат: '+(asset.package_format || 'не проверен')+(asset.has_preview?' · С аудиодемо':'')));
    const buttons=element('div','review-actions');
    for (const [action,label] of [['approved','Опубликовать'],['rejected','Отклонить']]) {
      const b=element('button',action==='approved'?'primary':'secondary',label);
      b.addEventListener('click',async()=>{
        if (action==='approved'&&!window.confirm('Вы проверили происхождение, безопасность и совместимость материала?')) return;
        try {await api('admin/assets/'+asset.id,{method:'PATCH',body:JSON.stringify({status:action})});notify('Статус сохранён',true);await refresh();}
        catch(e){notify(e.message);}
      });buttons.append(b);
    }
    card.append(buttons);pending.append(card);
  }
}
document.getElementById('load').addEventListener('click',async()=>{try{await refresh();notify('Данные загружены',true);}catch(e){notify(e.message);}});
document.getElementById('model-form').addEventListener('submit',async(e)=>{
 e.preventDefault();
 try {const data=Object.fromEntries(new FormData(e.target));await api('admin/models',{method:'POST',body:JSON.stringify(data)});notify('Модель добавлена',true);e.target.reset();await refresh();}catch(err){notify(err.message);}
});
document.getElementById('asset-form').addEventListener('submit',async(e)=>{
 e.preventDefault();const b=e.target.querySelector('[type=submit]');b.disabled=true;
 try {const data=new FormData(e.target);const item=await api('admin/assets',{method:'POST',body:data});notify('Файл загружен; SHA-256: '+item.sha256+'. Ожидает модерации.',true);e.target.reset();await refresh();}
 catch(err){notify(err.message);}finally{b.disabled=false;}
});

document.getElementById('manufacturer-form').addEventListener('submit',async(e)=>{
  e.preventDefault();
  try {
    const payload=Object.fromEntries(new FormData(e.target));
    await api('admin/manufacturers',{method:'POST',body:JSON.stringify(payload)});
    notify('Производитель добавлен',true);e.target.reset();await refresh();
  }catch(err){notify(err.message);}
});
document.getElementById('variant-form').addEventListener('submit',async(e)=>{
  e.preventDefault();
  try {
    const raw=Object.fromEntries(new FormData(e.target));
    const {identifier_kind,identifier_value,source_url,verification,...variant}=raw;
    variant.identifiers=identifier_value.trim()?[{kind:identifier_kind,value:identifier_value.trim(),source_url:source_url.trim(),verification}]:[];
    await api('admin/catalog/import',{method:'POST',body:JSON.stringify({variants:[variant]})});
    notify('Ревизия и идентификатор сохранены',true);e.target.reset();await refresh();
  }catch(err){notify(err.message);}
});

// Keep firmware metadata separate from voicepack-only fields.
const kindSelect=document.querySelector('#asset-form select[name="kind"]');
const voiceFields=document.getElementById('voicepack-fields');
function changeKind(){
  const voice=kindSelect.value==='voicepack';
  voiceFields.hidden=!voice;
  for(const name of ['series_slug','author','license'])document.querySelector('#asset-form [name="'+name+'"]').required=voice;
  document.querySelector('#asset-form [name="language"]').required=voice;
  document.querySelector('#asset-form [name="version"]').required=true;
  document.querySelector('#asset-form [name="region"]').required=!voice;
  document.querySelector('#asset-form [name="file"]').accept=voice?'.zip,.tar.gz,.tgz':'.zip,.pkg,.tar.gz,.tgz,.bin,.img';
}
kindSelect.addEventListener('change',changeKind);changeKind();
