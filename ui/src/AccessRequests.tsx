import React,{useCallback,useEffect,useRef,useState} from 'react';
import {ShieldCheck,X,ChevronRight} from 'lucide-react';
import {api,ApiError,Approval,Session} from './api';
import type {NotificationSettings} from './Inbox';
import {notificationPermission,sendChatNotification} from './platform';

export type PendingAccess=Approval&{workspace:string;host:string};
export function useAccessRequests(session:Session|undefined,sources:{name:string;url:string}[],settings:NotificationSettings){
 const[items,setItems]=useState<PendingAccess[]>([]);const[notice,setNotice]=useState<PendingAccess[]|null>(null);
 const live=useRef({settings,session});live.current={settings,session};
 const identity=session?.profile?.id;const sourceKey=JSON.stringify(session?.local?sources.map(source=>[source.name,new URL(source.url).host]):[['',location.host]]);
 const currentSourceKey=useRef(sourceKey);currentSourceKey.current=sourceKey;
 const busy=useRef<Promise<void>|null>(null);const again=useRef(false);const owner=useRef(identity);owner.current=identity;
 const refresh=useCallback(()=>{
  if(!identity||!session?.profile?.admin)return Promise.resolve();again.current=true;if(busy.current)return busy.current;
  const run=async()=>{while(again.current&&owner.current===identity){again.current=false;const list:PendingAccess[]=[];
   for(const[workspace,host]of JSON.parse(sourceKey) as string[][]){
    try{const result=await api<{requests:Approval[]}>('approvals'+(workspace?'?workspace='+encodeURIComponent(workspace):''));if(owner.current!==identity)return;list.push(...result.requests.map(q=>({...q,workspace,host})));}
    catch(error){if(!(error instanceof ApiError&&[401,403,404].includes(error.status)))return;}
   }
   if(currentSourceKey.current!==sourceKey)return;setItems(list);const prefs=live.current.settings;if(prefs.level==='none')continue;
   const fresh=list.filter(item=>!item.notified);if(!fresh.length)continue;
   const focused=document.visibilityState==='visible'&&document.hasFocus();
   if(!focused&&(!prefs.desktop||!(await notificationPermission()).granted))continue;
   if(owner.current!==identity)return;
   const claimed:PendingAccess[]=[];
   for(const[workspace]of JSON.parse(sourceKey) as string[][]){const pending=fresh.filter(q=>q.workspace===workspace);for(let offset=0;offset<pending.length;offset+=100){const page=pending.slice(offset,offset+100);if(!page.length)continue;const result=await api<{claimed:string[]}>('approvals'+(workspace?'?workspace='+encodeURIComponent(workspace):''),{IDs:page.map(q=>q.id)},'PATCH');claimed.push(...page.filter(q=>result.claimed.includes(q.id)));}}
   if(owner.current!==identity)return;if(!claimed.length)continue;
   if(focused){if(!document.querySelector('[role=dialog]'))setNotice(claimed);continue;}
   for(const[workspace]of JSON.parse(sourceKey) as string[][]){const group=claimed.filter(q=>q.workspace===workspace);if(!group.length)continue;await sendChatNotification({id:'requests:'+workspace,title:group.length>1?group.length+' agents want to connect':group[0].name+' wants to connect',body:'Review connection requests in '+group[0].host,target:'@me',root:'',action:'requests',workspace,sound:prefs.sound});}
  }};
  const promise=run().catch(()=>{}).finally(()=>{if(busy.current===promise)busy.current=null;});busy.current=promise;return promise;
 },[identity,session?.profile?.admin,sourceKey]);
 useEffect(()=>{setItems([]);setNotice(null);busy.current=null;again.current=false;void refresh();},[refresh]);
 useEffect(()=>{if(settings.desktop&&settings.level!=='none')void refresh();},[settings.desktop,settings.level,refresh]);
 useEffect(()=>{if(!notice)return;const timer=setTimeout(()=>setNotice(null),8000);return()=>clearTimeout(timer);},[notice]);
 useEffect(()=>{const expires=items.map(item=>item.expires_at||0).filter(at=>at>Date.now());if(!expires.length)return;const timer=setTimeout(()=>void refresh(),Math.min(...expires)-Date.now()+100);return()=>clearTimeout(timer);},[items,refresh]);
 return {items,notice,refresh,dismiss:()=>setNotice(null)};
}

export function AccessRequestSummary({items,onOpen}:{items:PendingAccess[];onOpen:(source:string)=>void}){
 if(!items.length)return null;
 return <section className="request-attention" aria-label="Pending connection requests"><header><ShieldCheck size={18}/><strong>Connection requests</strong><b>{items.length}</b></header>{items.map(q=><button key={q.workspace+':'+q.id} onClick={()=>onOpen(q.workspace)}><span><strong>{q.name}</strong><small>{q.client||'Agent'} · {q.host}</small></span><span>Review</span><ChevronRight size={15}/></button>)}</section>;
}
export function AccessRequestToast({items,onOpen,onClose}:{items:PendingAccess[];onOpen:(source:string)=>void;onClose:()=>void}){
 return <div className="attention-toast" role="status"><ShieldCheck size={21}/><button onClick={()=>{onClose();onOpen(items[0].workspace);}}><strong>{items.length>1?items.length+' agents want to connect':items[0].name+' wants to connect'}</strong><span>Review connection requests<ChevronRight size={13}/></span></button><button className="icon" aria-label="Dismiss connection request notification" onClick={onClose}><X size={16}/></button></div>;
}
