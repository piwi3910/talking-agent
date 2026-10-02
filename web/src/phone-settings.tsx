import React, { useEffect, useState } from 'react';

type Persona = { config: { id: string; name: string; organization: string }; users: { id: string; name: string }[] };
type Profile = { numbers: string[]; user_id: string; cues: boolean };
type Settings = { revision: number; personas: Record<string, Profile>; enabled: boolean };
async function request(body?: Settings): Promise<Settings> {
 const response = await fetch('/api/settings/phone', body ? { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : undefined);
 const data = await response.json();
 if (!response.ok) throw new Error(data.error || 'Could not load phone settings');
 return data;
}
export function PhoneSettings({ personas }: { personas: Persona[] }) {
 const [settings, setSettings] = useState<Settings>();
 const [numbers, setNumbers] = useState<Record<string,string>>({});
 const [error, setError] = useState('');
 const [notice, setNotice] = useState('');
 const [saving, setSaving] = useState(false);
 const [loading, setLoading] = useState(true);
 function adopt(data: Settings) { setSettings(data); setNumbers(Object.fromEntries(Object.entries(data.personas).map(([id,p])=>[id,p.numbers.join('\n')]))); }
 async function load() { setLoading(true); setError(''); try { adopt(await request()); } catch(e) { setError((e as Error).message); } finally { setLoading(false); } }
 useEffect(()=>{ void load(); },[]);
 function change(id:string, patch:Partial<Profile>) { setNotice(''); setSettings(old=>old ? {...old,personas:{...old.personas,[id]:{...old.personas[id],...patch}}} : old); }
 async function save(e:React.FormEvent) {
  e.preventDefault(); if(!settings)return; setSaving(true);setError('');setNotice('');
  const next={...settings,personas:Object.fromEntries(Object.entries(settings.personas).map(([id,p])=>[id,{...p,numbers:(numbers[id]||'').split(/[\n,]/).map(n=>n.trim()).filter(Boolean)}]))};
  try { adopt(await request(next)); setNotice('Phone settings saved. New calls use these assignments; current calls continue.'); } catch(e) { setError((e as Error).message); } finally { setSaving(false); }
 }
 return <section className="phone-settings" aria-label="Phone settings">
  <div className="section-heading"><h3>Phone settings</h3><span className="pill">{settings?.enabled?'SIP ready':'SIP not connected'}</span></div>
  <p className="muted">Give each persona its own phone numbers or extensions. Multiple phones can call different personas—or the same persona—at the same time.</p>
  {loading&&<p role="status">Loading phone settings…</p>}
  {error&&<p className="error" role="alert">{error}</p>}
  {notice&&<p className="settings-saved" role="status">{notice}</p>}
  {settings&&<form className="phone-settings-form" onSubmit={save}><fieldset disabled={saving}>
   {personas.map(a=>{const p=settings.personas[a.config.id];return p&&<div className="phone-persona" key={a.config.id}>
    <h4>{a.config.name} <small>{a.config.organization}</small></h4>
    <label>Phone numbers for {a.config.name}<textarea rows={3} value={numbers[a.config.id]||''} placeholder="500 or +32123456789" onChange={e=>{const value=e.target.value;setNotice('');setNumbers(old=>({...old,[a.config.id]:value}));}}/></label>
    <p className="quiet">One number per line. Leave empty to turn off incoming calls for this persona.</p>
    <label>Phone account for {a.config.name}<select value={p.user_id} onChange={e=>change(a.config.id,{user_id:e.target.value})}><option value="">No account · general questions</option>{a.users.map(u=><option value={u.id} key={u.id}>{u.name} · {u.id}</option>)}</select></label>
    <p className="quiet">Calls to this persona use this demo account for tools and memory, with no login prompt. Each call keeps its own conversation.</p>
    <label className="speech-toggle"><input type="checkbox" checked={p.cues} onChange={e=>change(a.config.id,{cues:e.target.checked})}/> Brief voice acknowledgements</label>
   </div>;})}
   <button className="primary" type="submit">{saving?'Saving…':'Save phone settings'}</button>
  </fieldset></form>}
  <button type="button" disabled={saving||loading} onClick={()=>{setNotice('');void load();}}>Reload saved settings</button>
  <p className="quiet">Your PBX must route each number to this agent service. You can interrupt replies and say “stop.” Changes are read aloud for a spoken “confirm” or “cancel.”</p>
 </section>;
}
