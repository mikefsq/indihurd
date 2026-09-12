'use strict';
(() => {
 const $=id=>document.getElementById('wm-'+id), form=$('form');
 if(!form)return;
 let profiles=[],drivers=[],busy=false,dirty=false,activeProfile='';
 async function api(path,method='GET',data){const r=await fetch('/api/'+path,{method,headers:{'Content-Type':'application/json'},body:data===undefined?undefined:JSON.stringify(data)});const v=await r.json();if(!r.ok)throw Error(v.detail||r.statusText);return v;}
 function route(){return 'profiles/'+encodeURIComponent($('profile').value);}
 function selected(){return profiles.find(p=>p.name===$('profile').value);}
 function draw(){const p=selected();$('name').value=p?.name||'';$('name').readOnly=!!p;$('port').value=p?.port||7624;$('autoconnect').checked=p?.autoconnect===1;$('autostart').checked=p?.autostart===1;const labels=new Set((p?.drivers||[]).map(d=>d.label));$('drivers').replaceChildren();for(const d of drivers){const label=document.createElement('label'),input=document.createElement('input');input.type='checkbox';input.value=d.label;input.checked=labels.has(d.label);label.append(input,document.createTextNode(' '+d.label));$('drivers').append(label);}filter();dirty=false;}
 function filter(){const q=$('filter').value.toLowerCase();for(const label of $('drivers').children)label.hidden=!label.textContent.toLowerCase().includes(q);}
 async function refresh(){const [status,running]=await Promise.all([api('server/status'),api('server/drivers')]);const s=status[0];activeProfile=s.active_profile;$('status').textContent=s.status==='True'?'Running: '+(s.active_profile||'Configured devices')+' · '+running.map(d=>d.label).join(', '):'No profile running';$('stop').disabled=!s.active_profile;}
 async function load(name=$('profile').value){[profiles,drivers]=await Promise.all([api('profiles'),api('drivers')]);$('profile').replaceChildren(new Option('New profile',''));for(const p of profiles)$('profile').append(new Option(p.name,p.name));$('profile').value=name;draw();await refresh();}
 async function run(fn){if(busy)return;busy=true;$('error').hidden=true;const buttons=[...form.querySelectorAll('button')];buttons.forEach(b=>b.disabled=true);try{await fn();}catch(e){$('error').textContent=e.message;$('error').hidden=false;}finally{buttons.forEach(b=>b.disabled=false);$('stop').disabled=!activeProfile;busy=false;}}
 form.addEventListener('input',()=>dirty=true);
 $('filter').addEventListener('input',filter);
 $('profile').addEventListener('change',draw);
 form.addEventListener('submit',e=>{e.preventDefault();run(async()=>{const name=$('name').value.trim();if(!name)throw Error('Enter a profile name');const path='profiles/'+encodeURIComponent(name);await api(path,'POST');await api(path,'PUT',{port:Number($('port').value),autostart:$('autostart').checked?1:0,autoconnect:$('autoconnect').checked?1:0});await api(path+'/drivers','POST',[...$('drivers').querySelectorAll('input:checked')].map(i=>({label:i.value})));await load(name);});});
 $('start').addEventListener('click',()=>run(async()=>{if(!selected()||dirty)throw Error('Save the profile before starting');await api('server/start/'+encodeURIComponent(selected().name),'POST');await refresh();}));
 $('stop').addEventListener('click',()=>run(async()=>{await api('server/stop','POST');await refresh();}));
 $('delete').addEventListener('click',()=>run(async()=>{if(!selected())throw Error('Select a saved profile');if(!confirm('Delete profile '+selected().name+'?'))return;await api(route(),'DELETE');await load('');}));
 $('refresh').addEventListener('click',()=>run(refresh));
 run(()=>load());
})();
