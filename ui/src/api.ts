export type Profile = { id: string; name: string; kind: 'human'; admin: boolean };
export type Session = { authenticated: boolean; profile?: Profile; local: boolean };
export type Channel = { name: string; label: string; source: string; source_id?:string;account_name?:string; unread: number; write: boolean; open: boolean; joined?:boolean; archived?:boolean;manage?:boolean;muted?:boolean;read_position?:number;read_held?:boolean };
export type Member = Person & {grants:{channel:string;write:boolean}[];admin:boolean;local?:boolean;self?:boolean};
export type DirectConversation=Channel&{kind:'human'|'agent';peer_id:string};
export type UploadedFile={id:string;name:string;type:string;size:number;image:boolean;url:string};
export type Person = { id: string; name: string; kind: 'human' | 'agent'; status: string;runtime?:string;role?:string;description?:string;owner_id?:string;owner_name?:string;aliases?:string[];can_edit?:boolean;last_seen?:number;last_message?:number;muted?:boolean;unread?:number };
export type Message = { seq: number; id: string; sender: string;profile_id?:string; target: string; body: string; chain_root: string; parent_id: string | null; created_at: number; kind: string; mine: boolean; pending: boolean;reply_count?:number;reply_unread?:number;last_reply_at?:number;reply_following?:boolean };
export type Approval = { id:string;profile_id:string;name:string;code:string;channels:string[];client?:string;redirect?:string;expires_at?:number;notified?:boolean };
export class ApiError extends Error { constructor(public status: number, message: string) { super(message); } }
const routes: Record<string,string> = {
 activity:'/v1/chat/activity',threads:'/v1/chat/threads','notification-settings':'/v1/chat/notification-settings','direct-conversations':'/v1/chat/direct-conversations',
 session:'/v1/auth/session',login:'/v1/auth/login',signup:'/v1/accounts',logout:'/v1/auth/logout',bootstrap:'/v1/auth/local-session',
 preferences:'/v1/chat/preferences',channels:'/v1/chat/channels',messages:'/v1/chat/messages',read:'/v1/chat/read-markers',create:'/v1/chat/channels',invite:'/v1/chat/invitations',join:'/v1/chat/memberships',memberships:'/v1/chat/memberships',members:'/v1/members',people:'/v1/chat/participants',approvals:'/v1/access-requests',sources:'/v1/workspaces','connect/status':'/v1/workspace-connections'
};
export async function api<T>(path: string, body?: unknown, method?:string): Promise<T> {
  const [name,query] = path.split('?');
  const endpoint=name.startsWith('people/')?'/v1/chat/participants/'+name.slice(7):name.startsWith('members/')?'/v1/members/'+name.slice(8):name.startsWith('connections/') ? '/v1/workspace-connections/'+name.slice(12) : name==='sources'&&body!==undefined ? '/v1/workspace-connections' : name==='approvals'&&body!==undefined&&method!=='PATCH' ? '/v1/access-approvals' : routes[name];
  const response = await fetch(endpoint + (query ? '?' + query : ''), { credentials: 'same-origin',method:method||(body===undefined?'GET':'POST'), ...(body !== undefined ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}) });
  const data = await response.json();
  if (!response.ok) throw new ApiError(response.status, data.error || 'Something went wrong. Try again.');
  return data;
}
export function fileURL(id:string,target:string){return '/v1/files/'+id+'?target='+encodeURIComponent(target);}
export async function uploadFile(file:File,target:string):Promise<UploadedFile>{
 const body=new FormData();body.set('target',target);body.set('file',file);
 const response=await fetch('/v1/files',{method:'POST',body,credentials:'same-origin'});const result=await response.json();if(!response.ok)throw new ApiError(response.status,result.error||'Upload failed');return result;
}
export function initials(name: string) { return name.split(/[-_ .]/).filter(Boolean).slice(0, 2).map(s => s[0].toUpperCase()).join(''); }
export function color(name: string) { let n = 0; for (const c of name) n = (n * 31 + c.charCodeAt(0)) | 0; return ['sage', 'blue', 'amber', 'violet', 'rose'][Math.abs(n) % 5]; }
export function shortName(name: string) { return name.split('@',1)[0].replace(/~.*/, ''); }
// Canonical addresses remain intact for routing. A short mention resolves only
// when it identifies one participant; duplicate handles must be qualified.
export function mentionNames(names:string[]){
  const canonical=new Set(names.filter(Boolean));const aliases=new Map<string,string|null>();
  for(const name of canonical){const short=shortName(name);if(!canonical.has(short))aliases.set(short,aliases.has(short)&&aliases.get(short)!==name?null:name);}
  return new Map([...Array.from(canonical,name=>[name,name] as const),...Array.from(aliases).filter((entry):entry is [string,string]=>entry[1]!==null)]);
}

export function mergeMessagePage(previous:Message[],incoming:Message[],delta:boolean,readingThread?:string){
 const all=new Map(previous.map(message=>[message.id,message]));const pageIDs=new Set(incoming.map(message=>message.id));const added=new Map<string,{total:number;unread:number}>();
 if(delta)for(const message of incoming){if(!message.parent_id||all.has(message.id)||pageIDs.has(message.chain_root))continue;const count=added.get(message.chain_root)||{total:0,unread:0};count.total++;if(!message.mine)count.unread++;added.set(message.chain_root,count);}
 for(const message of incoming)all.set(message.id,message);
 for(const[root,count]of added){const parent=all.get(root);if(!parent)continue;const known=parent.reply_count??previous.filter(message=>message.chain_root===root&&!!message.parent_id).length;all.set(root,{...parent,reply_count:known+count.total,last_reply_at:Math.max(parent.last_reply_at||0,...incoming.filter(message=>message.chain_root===root).map(message=>message.created_at)),reply_unread:readingThread===root?0:(parent.reply_unread||0)+(parent.reply_following?count.unread:0)});}
 return Array.from(all.values()).sort((a,b)=>a.seq-b.seq);
}
