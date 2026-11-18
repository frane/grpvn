import React,{useEffect,useId,useLayoutEffect,useRef,useState} from 'react';
import {Bell,Bot,Check,ChevronDown,ExternalLink,Globe,Hash,Laptop,LogOut,Plus,Settings2,ShieldCheck,Users} from 'lucide-react';

export type WorkspaceSource={name:string;url:string};
type Props={local:boolean;workspace:string;sources:WorkspaceSource[];account:string;admin:boolean;canInvite:boolean;requestCounts?:Record<string,number>;onSelect:(name:string)=>void;onAction:(action:string)=>void;onSignOut:()=>void};

export function WorkspaceMenu({local,workspace,sources,account,admin,canInvite,requestCounts={},onSelect,onAction,onSignOut}:Props){
 const[open,setOpen]=useState(false);
 const root=useRef<HTMLDivElement>(null);
 const trigger=useRef<HTMLButtonElement>(null);
 const menu=useRef<HTMLDivElement>(null);
 const initialFocus=useRef<'first'|'last'|'selected'>('selected');
 const id=useId();
 const source=sources.find(s=>s.name===workspace);
 const remote=!local||!!workspace;
 const label=remote?new URL(source?.url||location.origin).host:'Local chat';
 const subtitle=remote?(source?'Alias: '+source.name:'Remote workspace'):'On this computer';
 const items=()=>Array.from(menu.current?.querySelectorAll<HTMLButtonElement>('[role^="menuitem"]:not(:disabled)')||[]);
 function close(restore=false){setOpen(false);if(restore)trigger.current?.focus();}
 function action(name:string){close(true);onAction(name);}
 function show(focus:'first'|'last'|'selected'){initialFocus.current=focus;setOpen(true);}
 useLayoutEffect(()=>{if(!open)return;const buttons=items();const selected=menu.current?.querySelector<HTMLButtonElement>('[aria-checked="true"]');(initialFocus.current==='last'?buttons.at(-1):initialFocus.current==='selected'?selected||buttons[0]:buttons[0])?.focus();},[open]);
 useEffect(()=>{
  if(!open)return;
  const outside=(e:PointerEvent)=>{if(!root.current?.contains(e.target as Node))setOpen(false);};
  document.addEventListener('pointerdown',outside);
  return()=>document.removeEventListener('pointerdown',outside);
 },[open]);
 function navigate(e:React.KeyboardEvent){
  if(e.key==='Escape'){e.preventDefault();e.stopPropagation();close(true);return;}
  if(e.key==='Tab'){close(true);return;}
  const buttons=items();const current=buttons.indexOf(document.activeElement as HTMLButtonElement);
  if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){
   e.preventDefault();
   const next=e.key==='Home'?0:e.key==='End'?buttons.length-1:(current+(e.key==='ArrowDown'?1:-1)+buttons.length)%buttons.length;
   buttons[next]?.focus();
  }else if(e.key.length===1&&!e.altKey&&!e.ctrlKey&&!e.metaKey&&e.key!==' '){
   const ordered=[...buttons.slice(current+1),...buttons.slice(0,current+1)];
   const match=ordered.find(button=>(button.getAttribute('aria-label')||button.textContent||'').trim().toLowerCase().startsWith(e.key.toLowerCase()));
   if(match){e.preventDefault();match.focus();}
  }
 }
 return <div className="workspace-switcher" ref={root}>
  <button ref={trigger} className="workspace-head" aria-label="Workspace menu" aria-haspopup="menu" aria-expanded={open} aria-controls={open?id:undefined} onClick={()=>open?close():show('selected')} onKeyDown={e=>{if(e.key==='ArrowDown'||e.key==='ArrowUp'){e.preventDefault();show(e.key==='ArrowDown'?'first':'last');}}}>
   <span className="source-symbol">{remote?<Globe size={16}/>:<Laptop size={16}/>}</span><span className="workspace-title">{label}<span>{subtitle}</span></span>{Object.values(requestCounts).some(Boolean)&&<b className="request-badge" aria-label={Object.values(requestCounts).reduce((a,b)=>a+b,0)+' pending connection requests'}>{Object.values(requestCounts).reduce((a,b)=>a+b,0)}</b>}<ChevronDown size={15}/>
  </button>
  {open&&<div ref={menu} id={id} className="workspace-menu" role="menu" aria-label="Workspace menu" onKeyDown={navigate} onBlur={e=>{if(e.relatedTarget&&!root.current?.contains(e.relatedTarget as Node))close();}}>
   {local&&<>
    <div role="group" aria-label="Switch workspace">
     <div className="menu-label">Workspaces</div>
     <div className="workspace-choices">
      {[{name:'',url:''},...sources].map(s=>{const name=s.name?new URL(s.url).host:'Local chat';return <button key={s.name} role="menuitemradio" tabIndex={-1} aria-label={name} aria-checked={workspace===s.name} onClick={()=>{close(true);onSelect(s.name);}}>
       {s.name?<Globe size={17}/>:<Laptop size={17}/>}<span className="menu-workspace-name">{name}<small>{s.name?s.name+' · '+s.url:'On this computer'}</small></span>{!!requestCounts[s.name]&&<b className="request-badge">{requestCounts[s.name]}</b>}{workspace===s.name&&<Check size={16}/>}
      </button>;})}
     </div>
     <button role="menuitem" tabIndex={-1} onClick={()=>action('connect')}><Plus size={17}/><span>Add workspace…</span></button>
    </div>
    <div className="menu-rule" role="separator"/>
   </>}
   <div className="menu-workspace-summary"><strong>{label}</strong>{account&&<span>@{account}{admin?' · Administrator':''}</span>}</div>
   <div role="group" aria-label="Workspace actions">
    <button role="menuitem" tabIndex={-1} onClick={()=>action('agent')}><Bot size={17}/><span>Connect an agent…</span></button>
    <button role="menuitem" tabIndex={-1} disabled={!canInvite} onClick={()=>action('invite')}><Users size={17}/><span>Invite people or agents…</span></button>
    <button role="menuitem" tabIndex={-1} onClick={()=>action('create')}><Plus size={17}/><span>Create channel…</span></button>
    <button role="menuitem" tabIndex={-1} onClick={()=>action('channels')}><Hash size={17}/><span>Browse channels</span></button>
    <button role="menuitem" tabIndex={-1} onClick={()=>action('notifications')}><Bell size={17}/><span>Notification settings…</span></button>
    {local&&remote&&<button role="menuitem" tabIndex={-1} onClick={()=>action('browser')}><ExternalLink size={17}/><span>Open workspace in browser</span></button>}
   </div>
   {admin&&<>
    <div className="menu-rule" role="separator"/>
    <div role="group" aria-label="Administration"><div className="menu-label">Administration</div>
     <button role="menuitem" tabIndex={-1} onClick={()=>action('members')}><Settings2 size={17}/><span>Manage members…</span></button>
     {remote&&<button role="menuitem" aria-label="Connection requests…" tabIndex={-1} onClick={()=>action('approvals')}><ShieldCheck size={17}/><span>Connection requests…</span>{!!requestCounts[workspace]&&<b className="request-badge">{requestCounts[workspace]}</b>}</button>}
    </div>
   </>}
   {!local&&<><div className="menu-rule" role="separator"/><button role="menuitem" tabIndex={-1} onClick={()=>{close(true);onSignOut();}}><LogOut size={17}/><span>Sign out of this workspace</span></button></>}
  </div>}
 </div>;
}
