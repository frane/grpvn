import React,{useEffect,useRef,useState} from 'react';
import {Bot,Check,ChevronDown,Globe,Hash,Laptop,LogOut,Plus,Settings2,ShieldCheck,Users} from 'lucide-react';

export type WorkspaceSource={name:string;url:string};
export function WorkspaceMenu({local,workspace,sources,account,admin,onSelect,onAction,onSignOut}:{local:boolean;workspace:string;sources:WorkspaceSource[];account:string;admin:boolean;onSelect:(name:string)=>void;onAction:(action:string)=>void;onSignOut:()=>void}){
 const[open,setOpen]=useState(false);const root=useRef<HTMLDivElement>(null);
 useEffect(()=>{if(!open)return;const close=(e:PointerEvent)=>{if(!root.current?.contains(e.target as Node))setOpen(false);};const key=(e:KeyboardEvent)=>{if(e.key==='Escape'){setOpen(false);root.current?.querySelector('button')?.focus();}};document.addEventListener('pointerdown',close);document.addEventListener('keydown',key);return()=>{document.removeEventListener('pointerdown',close);document.removeEventListener('keydown',key);};},[open]);
 const source=sources.find(s=>s.name===workspace);const label=local?(source?new URL(source.url).host:'On this computer'):location.host;
 function action(name:string){setOpen(false);onAction(name);}
 return <div className="workspace-switcher" ref={root}><button className="workspace-head" aria-label="Workspace menu" aria-haspopup="menu" aria-expanded={open} onClick={()=>setOpen(!open)}><span className="workspace-mark">{local&&!workspace?<Laptop size={20}/>:<Globe size={20}/>}</span><span className="workspace-title">grpvn<span>{label}</span></span><ChevronDown size={17}/></button>{open&&<div className="workspace-menu" role="menu" aria-label="Workspace menu">
  {local&&<><div className="menu-label">Chat sources</div>{[{name:'',url:''},...sources].map(s=><button key={s.name} role="menuitemradio" aria-checked={workspace===s.name} onClick={()=>{setOpen(false);onSelect(s.name);}}>{s.name?<Globe size={17}/>:<Laptop size={17}/>}<span>{s.name?new URL(s.url).host:'On this computer'}</span>{workspace===s.name&&<Check size={16}/>}</button>)}<div className="menu-rule"/></>}
  <div className="menu-label">{label}</div>
  <button role="menuitem" onClick={()=>action('agent')}><Bot size={17}/><span>Connect an agent</span></button>
  <button role="menuitem" onClick={()=>action('create')}><Plus size={17}/><span>Create channel</span></button>
  <button role="menuitem" onClick={()=>action('channels')}><Hash size={17}/><span>Browse channels</span></button>
  <button role="menuitem" onClick={()=>action('invite')}><Users size={17}/><span>Invite people or agents</span></button>
  {admin&&<><button role="menuitem" onClick={()=>action('members')}><Settings2 size={17}/><span>Manage members</span></button><button role="menuitem" onClick={()=>action('approvals')}><ShieldCheck size={17}/><span>Connection requests</span></button></>}
  {local&&<><div className="menu-rule"/><button role="menuitem" onClick={()=>action('connect')}><Plus size={17}/><span>Add workspace</span></button></>}
  {!local&&<><div className="menu-rule"/><div className="menu-account">Signed in as <strong>{account}</strong></div><button role="menuitem" onClick={()=>{setOpen(false);onSignOut();}}><LogOut size={17}/><span>Sign out</span></button></>}
 </div>}</div>;
}
