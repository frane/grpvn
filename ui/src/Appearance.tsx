import React,{useCallback,useEffect,useLayoutEffect,useState} from 'react';
import {Laptop,Moon,Sun,X} from 'lucide-react';
import {api} from './api';

type Mode='system'|'light'|'dark';
function initialMode():Mode{try{const saved=localStorage.getItem('grpvn.appearance');if(saved==='light'||saved==='dark')return saved;}catch{}return 'system';}
function apply(mode:Mode){document.documentElement.dataset.theme=mode==='system'?(matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light'):mode;}
apply(initialMode());

export function useAppearance(authenticated:boolean){
 const[mode,setMode]=useState<Mode>(initialMode);
 const refresh=useCallback(async()=>{const result=await api<{mode:Mode}>('appearance');if(['system','light','dark'].includes(result.mode))setMode(result.mode);},[]);
 useEffect(()=>{if(authenticated)void refresh().catch(()=>{});},[authenticated,refresh]);
 useLayoutEffect(()=>{apply(mode);try{localStorage.setItem('grpvn.appearance',mode);}catch{}const media=matchMedia('(prefers-color-scheme: dark)');const changed=()=>apply(mode);media.addEventListener('change',changed);return()=>media.removeEventListener('change',changed);},[mode]);
 const save=useCallback(async(mode:Mode)=>{await api('appearance',{Mode:mode},'PATCH');setMode(mode);},[]);
 return {mode,save,refresh};
}

export function AppearanceSettings({mode,onChange,onClose}:{mode:Mode;onChange:(mode:Mode)=>Promise<void>;onClose:()=>void}){
 const[busy,setBusy]=useState(false);const[error,setError]=useState('');
 useEffect(()=>{const key=(event:KeyboardEvent)=>{if(event.key==='Escape')onClose();};document.addEventListener('keydown',key);return()=>document.removeEventListener('keydown',key);},[onClose]);
 async function choose(next:Mode){setBusy(true);setError('');try{await onChange(next);}catch(error){setError((error as Error).message);}finally{setBusy(false);}}
 return <div className="modal-backdrop" onMouseDown={event=>{if(event.target===event.currentTarget)onClose();}}><section className="modal appearance-settings" role="dialog" aria-modal="true" aria-label="Appearance"><button className="icon close" aria-label="Close appearance" onClick={onClose}><X size={19}/></button><h2>Appearance</h2><p>Choose a color scheme. System follows this device.</p><fieldset disabled={busy}><legend>Color scheme</legend>{([{value:'system',label:'System',description:'Follow the operating system',icon:Laptop},{value:'light',label:'Light',description:'Light surfaces and dark text',icon:Sun},{value:'dark',label:'Dark',description:'Dark surfaces and light text',icon:Moon}] as const).map(option=><label key={option.value} className={'appearance-choice '+(mode===option.value?'selected':'')}><option.icon size={22}/><span><strong>{option.label}</strong><small>{option.description}</small></span><input type="radio" name="appearance" value={option.value} aria-label={option.label} checked={mode===option.value} onChange={()=>void choose(option.value)}/></label>)}</fieldset>{error&&<p className="form-error" role="alert">{error}</p>}</section></div>;
}
