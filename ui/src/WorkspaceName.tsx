import React,{useEffect,useState} from 'react';
import {X} from 'lucide-react';
import {api} from './api';
export function WorkspaceName({source,name,local,onSaved,onClose}:{source:string;name:string;local:boolean;onSaved:()=>Promise<void>;onClose:()=>void}){
 const[value,setValue]=useState(name);const[busy,setBusy]=useState(false);const[error,setError]=useState('');
 useEffect(()=>{const key=(e:KeyboardEvent)=>{if(e.key==='Escape')onClose();};document.addEventListener('keydown',key);return()=>document.removeEventListener('keydown',key);},[onClose]);
 async function save(event:React.FormEvent){event.preventDefault();setBusy(true);setError('');try{await api('sources',{Source:source,Name:value.trim()},'PATCH');await onSaved();onClose();}catch(error){setError((error as Error).message);}finally{setBusy(false);}}
 return <div className="modal-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget)onClose();}}><section className="modal" role="dialog" aria-modal="true" aria-label="Rename workspace"><button className="icon close" aria-label="Close rename workspace" onClick={onClose}><X size={19}/></button><h2>Rename workspace</h2><p>{local?'Choose the name shown for this workspace on your computer.':'Everyone in this workspace sees this name.'}</p><form onSubmit={save}><label>Workspace name<input autoFocus required maxLength={80} value={value} onChange={event=>setValue(event.target.value)}/></label>{error&&<p className="form-error" role="alert">{error}</p>}<button className="button primary" disabled={busy||!value.trim()}>{busy?'Saving…':'Save name'}</button></form></section></div>;
}
