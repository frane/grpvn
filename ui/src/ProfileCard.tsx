import React,{useEffect,useState} from 'react';
import {Bot,MessageCircle,UserRound,X} from 'lucide-react';
import {api,Person,shortName} from './api';
import {ParticipantLabels,runtimeLabel} from './ParticipantLabels';

function ProfileEditor({person,workspace,onSaved,onCancel}:{person:Person;workspace:string;onSaved:(person:Person)=>void;onCancel:()=>void}){
 const[name,setName]=useState(shortName(person.name));const[role,setRole]=useState(person.role||'');const[runtime,setRuntime]=useState(person.runtime||'');const[description,setDescription]=useState(person.description||'');const[owner,setOwner]=useState(person.owner_id||'');
 const[humans,setHumans]=useState<Person[]>([]);const[busy,setBusy]=useState(false);const[error,setError]=useState('');
 useEffect(()=>{api<{people:Person[]}>('people'+(workspace?'?workspace='+encodeURIComponent(workspace):'')).then(result=>setHumans(result.people.filter(person=>person.kind==='human'&&person.status==='active'))).catch(error=>setError(error.message));},[workspace]);
 async function save(event:React.FormEvent){event.preventDefault();setBusy(true);setError('');try{const saved=await api<Person>('people/'+encodeURIComponent(person.id)+(workspace?'?workspace='+encodeURIComponent(workspace):''),{Name:name,Role:role,Runtime:runtime,Description:description,OwnerID:owner},'PATCH');onSaved(saved);}catch(error){setError((error as Error).message);}finally{setBusy(false);}}
 return <form className="profile-editor" onSubmit={save}><h2>Edit agent profile</h2><p>The same identity, history, channel access and sessions stay attached.</p>
  <label>Name<input required maxLength={100} value={name} onChange={event=>setName(event.target.value)}/></label>
  <div className="profile-fields"><label>Role<input maxLength={80} placeholder="e.g. coding" value={role} onChange={event=>setRole(event.target.value)}/></label><label>Agent type<input list="agent-runtimes" maxLength={80} placeholder="e.g. Codex" value={runtime} onChange={event=>setRuntime(event.target.value)}/></label></div>
  <datalist id="agent-runtimes">{['Codex','Claude Code','Claude Desktop','ChatGPT','Gemini','Grok','OpenCode','Pi','Antigravity'].map(runtime=><option key={runtime} value={runtime}/>)}</datalist>
  <label htmlFor="profile-owner">Owner</label><select id="profile-owner" value={owner} onChange={event=>setOwner(event.target.value)}><option value="">Unassigned</option>{humans.map(human=><option key={human.id} value={human.id}>{human.name}</option>)}</select>
  <label>What this agent does<textarea rows={3} maxLength={1200} value={description} onChange={event=>setDescription(event.target.value)}/></label>
  {error&&<p className="form-error" role="alert">{error}</p>}<div className="profile-edit-actions"><button type="button" className="button secondary" disabled={busy} onClick={onCancel}>Cancel</button><button className="button primary" disabled={busy}>{busy?'Saving…':'Save profile'}</button></div>
 </form>;
}

export function ProfileCard({name,workspace,channel,source,known,onClose,onMessage}:{name:string;workspace:string;channel:string;source:string;known?:Person;onClose:()=>void;onMessage:(name?:string)=>void}){
 const[person,setPerson]=useState(known);const[failed,setFailed]=useState(false);const[editing,setEditing]=useState(false);
 useEffect(()=>{let alive=true;const query=new URLSearchParams({name,channel});if(workspace)query.set('workspace',workspace);api<{people:Person[]}>('people?'+query).then(result=>{if(alive)setPerson(result.people[0]||known);}).catch(()=>{if(alive)setFailed(true);});const key=(e:KeyboardEvent)=>{if(e.key==='Escape')onClose();};document.addEventListener('keydown',key);return()=>{alive=false;document.removeEventListener('keydown',key);};},[name,workspace,channel,known,onClose]);
 const timestamp=(value:number)=>new Intl.DateTimeFormat(undefined,{dateStyle:'medium',timeStyle:'short'}).format(value);const display=person?.name||name;
 return <div className="modal-backdrop profile-backdrop" onMouseDown={e=>{if(e.target===e.currentTarget)onClose();}}><section className="profile-card" role="dialog" aria-modal="true" aria-label={'Profile @'+name}><button className="icon close" aria-label="Close profile" onClick={onClose}><X size={19}/></button>{editing&&person?<ProfileEditor person={person} workspace={workspace} onSaved={person=>{setPerson(person);setEditing(false);}} onCancel={()=>setEditing(false)}/>:<>
  <span className="profile-type-icon">{person?.kind==='human'?<UserRound size={26}/>:<Bot size={26}/>}</span><h2>@{shortName(display)}</h2><p>{person?.kind==='human'?'Human':person?.kind==='agent'?'AI agent':'Participant'}</p><ParticipantLabels person={person}/>{person?.description&&<p className="profile-description">{person.description}</p>}
  <dl>{person?.kind==='agent'&&<><div><dt>Role</dt><dd>{person.role||'Not assigned'}</dd></div><div><dt>Agent type</dt><dd>{person.runtime?runtimeLabel(person.runtime):'Not reported'}</dd></div><div><dt>Owner</dt><dd>{person.owner_name||'Not assigned'}</dd></div></>}<div><dt>DM address</dt><dd>@{display}</dd></div><div><dt>Source</dt><dd>{source}</dd></div>{person?.last_seen?<div><dt>Last active</dt><dd>{timestamp(person.last_seen)}<small>Recent connection or runtime activity</small></dd></div>:<div><dt>Presence</dt><dd>{failed?'Unavailable while this source is offline':'Not reported'}</dd></div>}{!!person?.last_message&&<div><dt>Last message here</dt><dd>{timestamp(person.last_message)}</dd></div>}</dl>
  <button className="button primary wide" onClick={()=>onMessage(display)}><MessageCircle size={17}/>Message @{shortName(display)}</button>{person?.can_edit&&<button className="button secondary wide edit-profile" onClick={()=>setEditing(true)}>Edit agent profile</button>}
 </>}</section></div>;
}
