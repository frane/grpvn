import React,{useEffect,useState} from 'react';

// A durable outbox entry is the normal first step of sending. Avoid flashing
// an offline warning for the short interval before the server acknowledges it.
export function DeliveryStatus({pending,createdAt}:{pending:boolean;createdAt:number}){
 const[age,setAge]=useState(()=>Date.now()-createdAt);
 useEffect(()=>{if(!pending)return;const update=()=>setAge(Date.now()-createdAt);update();const early=setTimeout(update,Math.max(0,800-(Date.now()-createdAt)));const delayed=setTimeout(update,Math.max(0,8000-(Date.now()-createdAt)));return()=>{clearTimeout(early);clearTimeout(delayed);};},[pending,createdAt]);
 if(!pending||age<800)return null;
 return <span className="queued" title={age<8000?'Waiting for the server to acknowledge this message.':'Saved on this device; awaiting server confirmation. Retries continue in the background.'}>{age<8000?'Sending…':'Not sent yet'}</span>;
}
