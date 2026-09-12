'use strict';
(() => {
 const profileForm=document.getElementById('profile-selection'),profileSelect=document.getElementById('active-profile');
 let applyingProfile=false;
 if(profileForm){profileSelect.addEventListener('change',()=>{if(!applyingProfile)profileForm.requestSubmit();});profileForm.addEventListener('submit',()=>{applyingProfile=true;profileSelect.setAttribute('aria-busy','true');});}
 const settings=document.getElementById('settings-form');
 if(settings){
  const mode=settings.elements.mode,port=settings.elements.indiPort,fields=document.getElementById('indi-settings'),check=document.getElementById('settings-check'),save=document.getElementById('settings-save'),status=document.getElementById('settings-validation');
  const blocked=check.disabled;let generation=0;
  function changed(){generation++;fields.hidden=mode.value==='alpaca';port.required=!fields.hidden;port.disabled=fields.hidden;save.disabled=true;check.disabled=blocked;status.textContent='';}
  settings.addEventListener('input',changed);changed();
  check.addEventListener('click',async()=>{if(!settings.reportValidity())return;const current=generation;check.disabled=true;status.textContent='Checking configuration…';try{const response=await fetch('/setup/settings/check',{method:'POST',body:new URLSearchParams(new FormData(settings))});const result=await response.json();if(current!==generation)return;save.disabled=!result.valid;status.textContent=result.error||result.message;status.className=result.valid?'ok':'error';}catch(_){if(current===generation){status.textContent='Check failed. Your changes are preserved.';status.className='error';}}finally{if(current===generation)check.disabled=blocked;}});
  settings.addEventListener('submit',event=>{if(event.submitter?.value==='stop-all')return;if(save.disabled)event.preventDefault();});
 }
 const editor=document.getElementById('config-editor');
 if(editor){
  const text=editor.querySelector('textarea'),save=document.getElementById('save'),check=document.getElementById('check'),syntax=document.getElementById('syntax'),validation=document.getElementById('validation');
  const alpacaPort=document.getElementById('alpaca-port'),deviceName=document.getElementById('device-name');
  const settingsPanel=document.getElementById('preconnect-settings'),propertyFields=document.getElementById('preconnect-fields');
  let propertyBindings=[],inspectedExec='',inspectedDevice='',sessionToken='',sessionBusy=false,sessionFault=false,propertySignature='';
  let generation=0,request;
  function changed(){generation++;if(request)request.abort();save.disabled=true;validation.textContent='';try{const draft=JSON.parse(text.value);if(alpacaPort&&document.activeElement!==alpacaPort)alpacaPort.value=draft.port??'';if(deviceName&&document.activeElement!==deviceName)deviceName.value=draft.name??'';syncProperties(draft);syntax.textContent='JSON syntax valid';syntax.className='ok';check.disabled=!editor.checkValidity()||sessionBusy||sessionFault}catch(error){syntax.textContent=error.message;syntax.className='error';check.disabled=true}}
  text.addEventListener('input',changed);changed();
  if(deviceName)deviceName.addEventListener('input',()=>{try{const draft=JSON.parse(text.value);draft.name=deviceName.value;text.value=JSON.stringify(draft,null,2)}catch(_){}changed()});
  if(alpacaPort)alpacaPort.addEventListener('input',()=>{
   if(!alpacaPort.validity.valid){changed();validation.textContent='Alpaca port must be a whole number between 1 and 65535.';return;}
   try{const draft=JSON.parse(text.value);draft.port=Number(alpacaPort.value);text.value=JSON.stringify(draft,null,2);changed()}catch(_){changed();}
  });
  check.addEventListener('click',async()=>{
   if(sessionFault||sessionBusy||!editor.reportValidity())return;
   const version=generation;request=new AbortController();check.disabled=true;validation.textContent='Checking configuration…';
   try{const response=await fetch('/setup/check',{method:'POST',body:new URLSearchParams(new FormData(editor)),signal:request.signal});const result=await response.json();if(version!==generation)return;save.disabled=!result.valid||!editor.checkValidity();validation.textContent=result.error||result.message;validation.className=result.valid?'ok':'error'}catch(error){if(version===generation){validation.textContent='Check failed. Your draft is preserved.';validation.className='error'}}finally{if(version===generation)check.disabled=!editor.checkValidity()||sessionBusy||sessionFault}
  });
  editor.addEventListener('submit',event=>{if(save.disabled)event.preventDefault()});
  const inspect=document.getElementById('inspect-driver'),inspection=document.getElementById('inspect-status');
  function setSession(token){sessionToken=token;const field=editor.elements.namedItem('inspection-session');if(field)field.value=token;}
  async function closeSession(){const token=sessionToken;setSession('');if(token)await fetch('/setup/inspect',{method:'POST',body:new URLSearchParams({session:token,op:'close'}),keepalive:true});}
  window.addEventListener('pagehide',()=>{if(sessionToken)navigator.sendBeacon('/setup/inspect',new URLSearchParams({session:sessionToken,op:'close'}));});
  function applySession(result){
   const draft=JSON.parse(text.value);
   if(draft.exec!==inspectedExec||draft.indi?.deviceName!==inspectedDevice)return;
   sessionFault=false;draft.indi.beforeConnect=result.beforeConnect;
   text.value=JSON.stringify(draft,null,2);renderProperties(result.properties||[],draft);changed();
   propertySignature=JSON.stringify(result.properties);inspection.textContent=result.message;inspection.className='ok';
  }
  async function updateSession(property){
   if(!sessionToken||sessionBusy)return;
   sessionBusy=true;save.disabled=true;check.disabled=true;
   for(const fieldset of propertyFields.querySelectorAll('fieldset'))fieldset.disabled=true;
   try{
    const draft=JSON.parse(text.value),body=new URLSearchParams({session:sessionToken,op:'set',property:property.Name});
    for(const member of property.Members)body.set('member.'+member.Name,draft.indi.beforeConnect[property.Name+'.'+member.Name]);
    const response=await fetch('/setup/inspect',{method:'POST',body});const result=await response.json();
    if(!response.ok)throw Error(result.error||'Driver update failed');applySession(result);
   }catch(error){sessionFault=true;inspection.textContent=error.message+' Draft retained; read settings again to resynchronize.';inspection.className='error';save.disabled=true;}
   finally{sessionBusy=false;for(const fieldset of propertyFields.querySelectorAll('fieldset'))fieldset.disabled=false;check.disabled=!editor.checkValidity()||sessionFault;}
  }
  if(inspect)setInterval(async()=>{
   if(!sessionToken||sessionBusy||document.hidden||propertyFields.contains(document.activeElement)||document.activeElement===text)return;
   sessionBusy=true;try{const response=await fetch('/setup/inspect',{method:'POST',body:new URLSearchParams({session:sessionToken,op:'read'})});const result=await response.json();
    if(!response.ok)throw Error(result.error);if(JSON.stringify(result.properties)!==propertySignature)applySession(result);
   }catch(error){sessionFault=true;inspection.textContent=error.message;inspection.className='error';save.disabled=true;}finally{sessionBusy=false;check.disabled=!editor.checkValidity()||sessionFault;}
  },2000);

  function syncProperties(draft){
   if(!settingsPanel)return;
   if(draft.exec!==inspectedExec||draft.indi?.deviceName!==inspectedDevice){
    settingsPanel.hidden=true;propertyFields.replaceChildren();propertyBindings=[];return;
   }
   for(const binding of propertyBindings){
    const values=draft.indi?.beforeConnect||{};
    if(binding.select){
     const selected=binding.property.Members.filter(member=>(values[binding.property.Name+'.'+member.Name]??(member.On?'On':'Off'))==='On');
     binding.select.value=selected.length===1?selected[0].Name:'';
     binding.select.setCustomValidity(selected.length>1?'Select only one option.':'');
    }else{
     for(const {input,member} of binding.inputs){
      const key=binding.property.Name+'.'+member.Name;
      const value=values[key]??(binding.property.Type==='Switch'?(member.On?'On':'Off'):member.Value);
      if(input.type==='checkbox')input.checked=value==='On';
      else if(document.activeElement!==input)input.value=value;
     }
    }
   }
  }
  function renderProperties(properties,draft){
   if(!settingsPanel)return;
   propertyFields.replaceChildren();propertyBindings=[];inspectedExec=draft.exec;inspectedDevice=draft.indi.deviceName;
   const groups=new Map();
   function element(tag,content,parent){const node=document.createElement(tag);if(content!==undefined)node.textContent=content;if(parent)parent.append(node);return node;}
   for(const property of properties){
    const group=property.Group||'General';
    if(!groups.has(group)){const section=element('section',undefined,propertyFields);element('h3',group,section);groups.set(group,section);}
    const fieldset=element('fieldset',undefined,groups.get(group));fieldset.className='preconnect-property';
    element('legend',property.Label||property.Name,fieldset);
    const description=element('p',property.State+' · '+(property.Writable?'Startup setting':'Read-only / managed by driver'),fieldset);description.className='muted';
    if(!property.Writable){
     for(const member of property.Members)element('p',(member.Label||member.Name)+': '+(property.Type==='Switch'?(member.On?'On':'Off'):member.Value),fieldset);
     continue;
    }
    const binding={property,inputs:[]};propertyBindings.push(binding);
    function update(){
     try{
      const draft=JSON.parse(text.value);draft.indi=draft.indi||{};draft.indi.beforeConnect=draft.indi.beforeConnect||{};
      if(binding.select){for(const member of property.Members)draft.indi.beforeConnect[property.Name+'.'+member.Name]=binding.select.value===member.Name?'On':'Off';binding.select.setCustomValidity('');}
      else for(const {input,member} of binding.inputs){
       if(input.type==='number'&&!input.validity.valid){changed();validation.textContent='Correct the highlighted numeric value before checking configuration.';return;}
       draft.indi.beforeConnect[property.Name+'.'+member.Name]=input.type==='checkbox'?(input.checked?'On':'Off'):input.value;
      }
      text.value=JSON.stringify(draft,null,2);changed();updateSession(property);
     }catch(_){validation.textContent='Correct the advanced JSON before editing these settings.';}
    }
    if(property.Type==='Switch'&&property.Rule!=='AnyOfMany'){
     const label=element('label',property.Label||property.Name,fieldset),select=element('select',undefined,label);binding.select=select;
     const empty=element('option',property.Rule==='AtMostOne'?'None':'Choose an option…',select);empty.value='';select.required=property.Rule==='OneOfMany';
     for(const member of property.Members){const option=element('option',member.Label||member.Name,select);option.value=member.Name;}
     select.addEventListener('change',update);
    }else for(const member of property.Members){
     const label=element('label',member.Label||member.Name,fieldset),input=element('input',undefined,label);
     input.type=property.Type==='Switch'?'checkbox':property.Type==='Number'?'number':'text';
     if(input.type==='number'){input.step=member.Step||'any';if(member.Min!=='')input.min=member.Min;if(member.Max!=='')input.max=member.Max;input.required=true;}
     input.addEventListener('change',update);binding.inputs.push({input,member});
    }
   }
   settingsPanel.hidden=false;syncProperties(draft);
  }
  let inspectionRequest,inspectionGeneration=0;
  async function readSettings(){
   if(!inspect)return;
   try{JSON.parse(text.value)}catch(_){inspection.textContent='Enter valid JSON first. Your draft is unchanged.';return;}
   const current=++inspectionGeneration;if(inspectionRequest)inspectionRequest.abort();inspectionRequest=new AbortController();
   const draftText=text.value;inspect.disabled=true;inspection.textContent='Reading pre-connect properties…';inspection.className='';
   try{
    await closeSession();
    const response=await fetch('/setup/inspect',{method:'POST',body:new URLSearchParams({text:draftText,session:'new'}),signal:inspectionRequest.signal});const result=await response.json();
    if(current!==inspectionGeneration)return;
    if(result.session)setSession(result.session);
    if(text.value!==draftText){await closeSession();inspection.textContent='Draft changed while reading. Read settings again to populate it.';return;}
    if(!response.ok)throw Error(result.error||'Could not read driver settings.');sessionFault=false;
    const draft=JSON.parse(draftText);draft.enable=false;if(!draft.name)draft.name=result.deviceName;draft.indi=draft.indi||{};draft.indi.deviceName=result.deviceName;
    sessionFault=false;draft.indi.beforeConnect=result.beforeConnect;
    text.value=JSON.stringify(draft,null,2);renderProperties(result.properties||[],draft);propertySignature=JSON.stringify(result.properties);changed();inspection.textContent=result.message;inspection.className='ok';
   }catch(error){if(current===inspectionGeneration){inspection.textContent=error.message+' Your draft is unchanged.';inspection.className='error';}}finally{if(current===inspectionGeneration)inspect.disabled=false;}
  }
  if(inspect)inspect.addEventListener('click',readSettings);
  const executable=document.getElementById('executable');if(executable)executable.addEventListener('change',()=>{if(!executable.value)return;try{const draft=JSON.parse(text.value);if(draft.exec!==executable.value){draft.indi={};draft.name="";draft.enable=false;}draft.exec=executable.value;text.value=JSON.stringify(draft,null,2);changed();readSettings()}catch(_){}});
  const mapping=document.getElementById('mapping');if(mapping){try{mapping.value=JSON.parse(text.value).driver}catch(_){}mapping.addEventListener('change',()=>{try{const draft=JSON.parse(text.value);draft.driver=mapping.value;text.value=JSON.stringify(draft,null,2);changed()}catch(_){}})}
 }
 const devices=document.getElementById('devices');
 if(devices){
  const filter=document.getElementById('state-filter'),refresh=document.getElementById('refresh'),note=document.getElementById('status-note');let busy=false;
  function apply(){for(const row of devices.querySelectorAll('[data-name]'))row.hidden=filter.value!=='all'&&row.dataset.enabled!==String(filter.value==='enabled');try{sessionStorage.setItem('indihurd-filter',filter.value)}catch(_){}}
  try{const saved=sessionStorage.getItem('indihurd-filter');if(['all','enabled','disabled'].includes(saved))filter.value=saved}catch(_){}filter.addEventListener('change',apply);apply();
  async function update(){if(busy||document.hidden)return;busy=true;refresh.disabled=true;try{const response=await fetch('/setup/status',{cache:'no-store'});if(!response.ok)throw Error();const result=await response.json();if(profileForm&&!applyingProfile){profileForm.elements.revision.value=result.revision;profileSelect.value=result.profile||'';}for(const row of devices.querySelectorAll('[data-name]')){const state=result.rows.find(item=>item.Name===row.dataset.name);if(!state)continue;row.dataset.enabled=String(state.Enabled);const toggle=row.querySelector('[role=switch]');toggle.setAttribute('aria-checked',String(state.Enabled));toggle.value=state.Enabled?'disable':'enable';const badge=row.querySelector('[data-status]');badge.textContent=state.State;badge.className='badge '+(['ok','warn','error'].includes(state.Kind)?state.Kind:'neutral');row.querySelector('[data-reason]').textContent=state.Reason;row.querySelector('[data-pending]').hidden=!state.Pending;for(const button of row.querySelectorAll('button[name=action]'))if(['restart','delete'].includes(button.value))button.disabled=button.value==='restart'?!state.Enabled:state.Enabled;}apply();note.textContent='Updated '+new Date().toLocaleTimeString()}catch(_){note.textContent='Status unavailable. Showing the previous values.'}finally{busy=false;refresh.disabled=false}}
  refresh.addEventListener('click',update);setInterval(update,5000);update();
 }
 const logs=document.getElementById('logs');if(logs){
  let paused=false,busy=false;const status=document.getElementById('log-status'),pause=document.getElementById('pause');
  async function update(){if(busy||document.hidden)return;busy=true;try{const response=await fetch('/setup/logs/tail?name='+encodeURIComponent(logs.dataset.name),{cache:'no-store'});if(!response.ok)throw Error();const lines=await response.json();const position=logs.scrollTop;logs.textContent=lines.map(line=>line.time+' '+line.device+': '+line.text).join('\n')||'No messages yet.';logs.scrollTop=document.getElementById('follow').checked?logs.scrollHeight:position;status.textContent='Updated '+new Date().toLocaleTimeString()}catch(_){status.textContent='Could not refresh logs; previous messages retained.'}finally{busy=false}}
  pause.addEventListener('click',()=>{paused=!paused;pause.textContent=paused?'Resume':'Pause';if(!paused)update()});document.getElementById('log-refresh').addEventListener('click',update);setInterval(()=>{if(!paused)update()},2000);update();
 }
})();
