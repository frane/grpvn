import {useCallback,useEffect,useLayoutEffect,useRef,type KeyboardEvent} from 'react';
import type {Message} from './api';

export type ConversationEntry={target:string;anchor:string;root?:string;offset?:number;held:boolean;version:string};
type Current<T>={current:T};
export function useConversationViewport({target,active,loading,messages,viewport,bottom,entry,readPosition,onVisible,hasNewer,onNewer}:{target:string;active:boolean;loading:boolean;messages:Message[];viewport:Current<HTMLDivElement|null>;bottom:Current<boolean>;entry:Current<ConversationEntry|null>;readPosition:number;onVisible:(target:string,position:number,resume:boolean,version:string)=>Promise<void>;hasNewer:boolean;onNewer:()=>void}){
 const content=useRef<HTMLDivElement>(null);const userMoved=useRef(false);const placing=useRef(false);const previous=useRef(target);const latest=useRef({onVisible,readPosition,hasNewer,onNewer});latest.current={onVisible,readPosition,hasNewer,onNewer};
 if(previous.current!==target){previous.current=target;userMoved.current=false;}
 const releaseAnchor=useCallback(()=>{if(entry.current)entry.current.anchor='';},[entry]);
 const input=useCallback(()=>{userMoved.current=true;releaseAnchor();},[releaseAnchor]);
 useLayoutEffect(()=>{
  const box=viewport.current;if(!box||!active||loading)return;
  const place=()=>{if(!box.isConnected)return;const initial=entry.current;placing.current=true;
   if(bottom.current)box.scrollTop=box.scrollHeight;
   else if(initial?.target===target&&initial.anchor){const node=box.querySelector<HTMLElement>('[data-message-id="'+initial.anchor+'"]')||box.querySelector<HTMLElement>('[data-message-id="'+initial.root+'"]');if(node)box.scrollTop+=node.getBoundingClientRect().top-box.getBoundingClientRect().top-(initial.offset??24);}
   requestAnimationFrame(()=>{placing.current=false;});
  };
  place();const observer=new ResizeObserver(place);if(content.current)observer.observe(content.current);observer.observe(box);return()=>observer.disconnect();
 },[active,loading,messages,target,viewport,bottom,entry]);
 useEffect(()=>{
  const box=viewport.current;if(!box||!active||loading)return;let stopped=false;let timer:ReturnType<typeof setTimeout>|undefined;let busy=false;let last=-1;let pending=0;
  const seen=new Map<string,number>();
  const flush=async()=>{timer=undefined;if(stopped||busy||document.visibilityState!=='visible'||!document.hasFocus())return;const position=Math.max(0,...seen.values());const initial=entry.current;const resume=!!initial?.held;if(position<=Math.max(last,latest.current.readPosition)&&!resume)return;pending=Math.max(pending,position);busy=true;const sending=pending;pending=0;last=Math.max(last,sending);try{await latest.current.onVisible(target,sending,resume,initial?.version||'');if(entry.current===initial&&initial)initial.held=false;}finally{busy=false;if(!stopped&&pending>last)timer=setTimeout(()=>void flush(),250);}};
  const schedule=()=>{if(!timer)timer=setTimeout(()=>void flush(),250);};
  const observer=new IntersectionObserver(items=>{for(const item of items){const id=(item.target as HTMLElement).dataset.messageId||'';const message=messages.find(message=>message.id===id);if(item.isIntersecting&&message)seen.set(id,message.seq);else seen.delete(id);}schedule();},{root:box,threshold:.15});
  for(const node of box.querySelectorAll<HTMLElement>('[data-message-id]'))observer.observe(node);
  const focused=()=>{if(document.visibilityState!=='visible')return;const bounds=box.getBoundingClientRect();seen.clear();for(const node of box.querySelectorAll<HTMLElement>('[data-message-id]')){const rect=node.getBoundingClientRect();if(rect.bottom>bounds.top+8&&rect.top<bounds.bottom-8){const message=messages.find(message=>message.id===node.dataset.messageId);if(message)seen.set(message.id,message.seq);}}schedule();};
  window.addEventListener('focus',focused);document.addEventListener('visibilitychange',focused);
  return()=>{stopped=true;observer.disconnect();if(timer)clearTimeout(timer);window.removeEventListener('focus',focused);document.removeEventListener('visibilitychange',focused);};
 },[active,loading,messages,target,viewport,entry]);
 const scrolled=useCallback(()=>{const box=viewport.current;if(!box||placing.current||!userMoved.current)return;const near=box.scrollHeight-box.scrollTop-box.clientHeight<100;bottom.current=near&&!latest.current.hasNewer;if(near&&latest.current.hasNewer)latest.current.onNewer();},[viewport,bottom]);
 const preparePrepend=useCallback(()=>{const box=viewport.current;if(!box)return;const bounds=box.getBoundingClientRect();const first=Array.from(box.querySelectorAll<HTMLElement>('[data-message-id]')).find(node=>node.getBoundingClientRect().bottom>bounds.top);if(!first)return;bottom.current=false;userMoved.current=false;entry.current={...entry.current,target,anchor:first.dataset.messageId!,offset:first.getBoundingClientRect().top-bounds.top,held:entry.current?.held||false,version:entry.current?.version||''};},[viewport,bottom,entry,target]);
 return {content,releaseAnchor,preparePrepend,events:{onScroll:scrolled,onWheel:input,onTouchMove:input,onPointerDown:input,onKeyDown:(event:KeyboardEvent)=>{if(['PageUp','PageDown','Home','End','ArrowUp','ArrowDown'].includes(event.key))input();}}};
}
