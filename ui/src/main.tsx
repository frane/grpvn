import React, { useCallback, useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Archive,BellOff,Mail,MailOpen,ArrowDown, ArrowLeft, ArrowUp, Bell, Bot, Check, CheckCheck, ChevronDown, ChevronRight, Copy, Globe, Hash, Link, LockKeyhole, LogOut, Menu, MessageCircle, MessageSquare, MoreHorizontal, PanelRightClose, Plus, Search, Settings2, ShieldCheck, Sparkles, Users, WifiOff, X } from 'lucide-react';
import { api, ApiError, Approval, Channel, color, initials, Message, Person, Session, shortName } from './api';
import './style.css';
import {openExternal} from './platform';
import {Composer} from './Composer';
const MessageBody=React.lazy(()=>import('./MessageBody').then(module=>({default:module.MessageBody})));
import {Management} from './Management';
import {BrandMark as Logo} from './Brand';
import {WorkspaceMenu,WorkspaceSource} from './WorkspaceMenu';
import {ConnectAgent} from './ConnectAgent';
import {ProfileCard} from './ProfileCard';
import {ContextAction,ContextMenu} from './ContextMenu';
const Presentation=React.createContext<{people:Person[];name:string;onMention:(name:string)=>void;onContext:(e:React.MouseEvent,m:Message)=>void}>({people:[],name:'',onMention:()=>{},onContext:()=>{}});

function Avatar({ name, human = false, small = false }: { name: string; human?: boolean; small?: boolean }) {
  return <span className={`avatar ${color(name)} ${small ? 'small' : ''}`} aria-hidden="true">{human ? initials(name) : <Bot size={small ? 14 : 19} strokeWidth={1.7} />}</span>;
}
function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [people, setPeople] = useState<Person[]>([]);
  const [participants,setParticipants]=useState<Person[]>([]);
  const [workspace,setWorkspace]=useState<string|null>(null);
  const workspaceSelection=useRef<string|null>(null);workspaceSelection.current=workspace;
  const [connections,setConnections]=useState<WorkspaceSource[]>([]);
  const [workspaceSession,setWorkspaceSession]=useState<Session|null>(null);
  const [agentSetup,setAgentSetup]=useState(false);
  const [context,setContext]=useState<{x:number;y:number;target:string;channel?:Channel;person?:Person;message?:Message;selection?:string}|null>(null);
  const closeContext=useCallback(()=>setContext(null),[]);
  const [readState,setReadState]=useState({position:0,held:false});
  const readPaused=useRef('');
  const [profileName,setProfileName]=useState<string|null>(null);
  const closeProfile=useCallback(()=>setProfileName(null),[]);
  const [detailsChannel,setDetailsChannel]=useState<Channel|undefined>();
  const [management,setManagement]=useState<'members'|'channel'|'channels'|'invite'|null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [historyChannel,setHistoryChannel]=useState<Channel|null>(null);
  const historySelection=useRef<Channel|null>(null);
  const [target, setTarget] = useState(new URLSearchParams(location.search).get('target')||localStorage.getItem('grpvn.target')||'#general');
  const [thread, setThread] = useState<Message | null>(null);
  const [tab, setTab] = useState<'chats' | 'people'>('chats');
  const [search, setSearch] = useState('');
  const [error, setError] = useState('');
  const [connected, setConnected] = useState(false);
  const [modal, setModal] = useState<'create' | 'invite' | 'join' | 'connect' | 'approvals' | null>(null);
  const closeModal = useCallback(()=>setModal(null),[]);
  const [creationSource,setCreationSource]=useState<string|undefined>();
  function createChannel(source?:string){setCreationSource(source);setModal('create');}
  const [sidebar, setSidebar] = useState(false);
  const [localChat,setLocalChat]=useState(false);
  const [setupPending,setSetupPending]=useState(false);
  const [loading, setLoading] = useState(false);
  const [more, setMore] = useState(false);
  const scroll = useRef<HTMLDivElement>(null);
  const bottom = useRef(true);
  const currentTarget = useRef(target);
  currentTarget.current = target;
  const selected = channels.find(c => c.name === target) || (historyChannel?.name===target?historyChannel:undefined);
  const activeWorkspace=workspace??selected?.source_id??'';
  const scopeChannels=channels.filter(c=>(c.source_id||'')===activeWorkspace);
  const sourceURL=connections.find(s=>s.name===activeWorkspace)?.url||location.origin;
  const sourceLabel=session?.local&&!activeWorkspace?'On this computer':new URL(sourceURL).host;
  const profile = session?.profile;
  const scopeAdmin=activeWorkspace?!!workspaceSession?.profile?.admin:!!profile?.admin;
  const activeName=selected?.account_name || profile?.name || "";
  const dm = target.startsWith('@');
  const label = dm ? target.slice(1) : (selected?.label || target).replace(/^#/, '');
  const [toast, setToast] = useState('');
  useEffect(() => { if (toast) { const t = setTimeout(() => setToast(''), 3500); return () => clearTimeout(t); } }, [toast]);
  const fail = useCallback((e: unknown) => { if (e instanceof ApiError && e.status === 401) setSession({ authenticated: false, local: false }); setError(e instanceof Error ? e.message : 'Could not connect'); }, []);
  const refreshSession = useCallback(async () => { try { const state=await api<Session>('session');setSession(state);const next=new URLSearchParams(location.search).get('next');if(state.authenticated&&next){const where=new URL(next,location.origin);if(where.origin===location.origin&&where.pathname==='/oauth/authorize'&&where.searchParams.get('actor')==='human'){location.replace(where.pathname+where.search);}} } catch (e) { fail(e); } }, [fail]);
  useEffect(() => {
    const ticket = new URLSearchParams(location.hash.slice(1)).get('ticket');
    if (ticket) { history.replaceState(null, '', '/'); api('bootstrap', { Ticket: ticket }).then(refreshSession).catch(e => { fail(e); refreshSession(); }); }
    else refreshSession();
  }, [refreshSession, fail]);
  const refreshLists = useCallback(async () => {
    const [c, p] = await Promise.all([api<{ channels: Channel[] }>('channels'), api<{ people: Person[] }>('people')]);
    setChannels(c.channels); setPeople(p.people);if(c.channels.length)setLocalChat(true);
    const source=workspaceSelection.current??c.channels.find(x=>x.name===currentTarget.current)?.source_id;
    if(source){try{const remote=await api<{people:Person[]}>('people?workspace='+encodeURIComponent(source));setParticipants(remote.people);}catch{setParticipants(p.people);}}else setParticipants(p.people);
    if(historySelection.current&&c.channels.some(c=>c.name===historySelection.current?.name)){historySelection.current=null;setHistoryChannel(null);}
    if (!currentTarget.current.startsWith('@') && !historySelection.current && !c.channels.some(x => x.name === currentTarget.current)) { const matching=c.channels.filter(c=>(c.source_id||'')===(source||''));setTarget(matching[0]?.name||''); }
  }, []);
  const refreshMessages = useCallback(async (where: string, fresh=false) => {
    if(!where){setMessages([]);setMore(false);return;}
    const page = await api<{ messages: Message[]; more: boolean;read_position:number;read_held:boolean }>('messages?target=' + encodeURIComponent(where)+(fresh?'&refresh=1':''));
    if (currentTarget.current !== where) return;
    setMessages(page.messages); setMore(page.more);setReadState({position:page.read_position||0,held:!!page.read_held});
    if (!page.read_held&&readPaused.current!==where&&page.messages.length && document.visibilityState === 'visible' && document.hasFocus()) api<{held:boolean}>('read', { Target: where, Position: Math.max(...page.messages.map(m => m.seq)) }).then(result=>{if(!result.held&&readPaused.current!==where){setChannels(prev=>prev.map(c=>c.name===where?{...c,unread:0}:c));setPeople(prev=>prev.map(p=>'@'+p.name===where?{...p,unread:0}:p));}}).catch(() => {});
  }, []);
  useEffect(() => {
    if (!session?.authenticated) return;
    refreshLists().catch(fail);
    const events = new EventSource('/v1/chat/events');
    events.addEventListener('connected', () => { setConnected(true); refreshLists().catch(fail); refreshMessages(currentTarget.current).catch(fail); });
    events.addEventListener('changed', () => { refreshLists().catch(fail); refreshMessages(currentTarget.current).catch(fail); });
    events.addEventListener('signed-out', () => { events.close(); refreshSession(); });
    events.onerror = () => setConnected(false);
    return () => events.close();
  }, [session?.authenticated, refreshLists, refreshMessages, refreshSession, fail]);
  useEffect(() => {
    if (!session?.authenticated) return;
    setThread(null); setError(''); setMessages([]); setLoading(true); bottom.current = true;
    localStorage.setItem('grpvn.target', target);
    refreshLists().catch(fail);refreshMessages(target).catch(fail).finally(() => setLoading(false));
  }, [target, session?.authenticated, refreshMessages, fail]);
  useEffect(()=>{if(!session?.authenticated)return;const focused=()=>{if(document.visibilityState==='visible'){refreshLists().catch(fail);refreshMessages(currentTarget.current).catch(fail);};};window.addEventListener('focus',focused);document.addEventListener('visibilitychange',focused);return()=>{window.removeEventListener('focus',focused);document.removeEventListener('visibilitychange',focused);};},[session?.authenticated,refreshLists,refreshMessages,fail]);
  const sourceKey=[...new Set(channels.map(c=>c.source_id||''))].sort().join(',');
  useEffect(()=>{if(!session?.authenticated||!session.local)return;api<{sources:WorkspaceSource[]}>('sources').then(result=>setConnections(result.sources)).catch(()=>{});},[session?.authenticated,session?.local,modal,sourceKey]);
  useEffect(()=>{if(workspace===null&&selected)setWorkspace(selected.source_id||'');},[workspace,target,selected?.source_id]);
  useEffect(()=>{setWorkspaceSession(null);if(!activeWorkspace)return;let alive=true;api<Session>('session?workspace='+encodeURIComponent(activeWorkspace)).then(value=>{if(alive)setWorkspaceSession(value);}).catch(()=>{});return()=>{alive=false;};},[activeWorkspace]);
  useEffect(() => { if (bottom.current) scroll.current?.scrollTo({ top: scroll.current.scrollHeight, behavior: 'instant' }); }, [messages]);
  async function send(body: string, reply?: Message, key?: string) {
    const result = await api<{ pending: boolean }>('messages', { Target: reply?.id || target, Body: body, IdempotencyKey: key || crypto.randomUUID() });
    await refreshMessages(target); await refreshLists(); setError(''); bottom.current = true;
    if (result.pending) setToast('Saved locally. Delivery will resume when the source is available.');
  }
  async function older() {
    if (!messages.length) return;
    try { const page = await api<{ messages: Message[]; more: boolean }>(`messages?target=${encodeURIComponent(target)}&before=${messages[0].seq}`); setMessages(prev => [...page.messages, ...prev]); setMore(page.more); } catch (e) { fail(e); }
  }
  function select(where: string, history?:Channel) { readPaused.current='';if(where)void api('read',{Target:where,Resume:true}).then(()=>refreshLists()).catch(fail);const match=channels.find(c=>c.name===where)||history;const scope=match?(match.source_id||''):null;workspaceSelection.current=scope;setWorkspace(scope);currentTarget.current=where;setLocalChat(true);historySelection.current=history||null;setHistoryChannel(history||null);setTarget(where);setTab('chats');setSidebar(false); }
  function selectWorkspace(name:string){const where=channels.find(c=>(c.source_id||'')===name)?.name||'';select(where);workspaceSelection.current=name;setWorkspace(name);}
  function workspaceAction(action:string){if(action==='agent')setAgentSetup(true);else if(action==='members'||action==='channels')setManagement(action);else if(action==='create')createChannel(activeWorkspace||undefined);else if(action==='invite')setManagement('invite');else setModal(action as 'connect'|'approvals');}
  function messagePerson(name:string){setProfileName(null);if(session?.local&&activeWorkspace){void openExternal(sourceURL.replace(/\/$/,'')+'/?target='+encodeURIComponent('@'+name));return;}select('@'+name);}
  function openContext(e:React.MouseEvent,where:string,channel?:Channel,person?:Person,message?:Message){e.preventDefault();e.stopPropagation();const box=e.currentTarget.getBoundingClientRect();setContext({x:e.clientX||box.left+12,y:e.clientY||box.bottom,target:where,channel,person,message,selection:window.getSelection()?.toString()||''});}
  async function markUnread(where:string,position?:number){const previous=readPaused.current;if(where===currentTarget.current)readPaused.current=where;try{await api('read',{Target:where,Unread:true,...(position!==undefined?{Position:position}:{})});await refreshLists();if(where===currentTarget.current)await refreshMessages(where);setToast('Marked as unread.');}catch(error){readPaused.current=previous;throw error;}}
  async function markRead(where:string){if(where===currentTarget.current)readPaused.current='';await api('read',{Target:where,Resume:true});await refreshLists();if(where===currentTarget.current)await refreshMessages(where);setToast('Marked as read.');}
  async function muteConversation(where:string,muted:boolean){await api('preferences',{Target:where,Muted:muted},'PATCH');await refreshLists();setToast(muted?'Conversation muted. Messages keep syncing.':'Conversation unmuted.');}
  async function channelAction(c:Channel,archive=false){const suffix=c.source_id?'?workspace='+encodeURIComponent(c.source_id):'';if(archive)await api('channels'+suffix,{Channel:c.name,Archived:true},'PATCH');else await api('memberships'+suffix,{Channel:c.name,Action:'leave'});await refreshLists();setToast(archive?'Channel archived. History is retained.':'Left '+c.label);}
  async function copyValue(value:string,label:string){await navigator.clipboard.writeText(value);setToast(label+' copied.');}
  const contextItems:ContextAction[]=[];
  if(context){const c=context.channel;const muted=!!(c?.muted||context.person?.muted);if(context.message){const m=context.message;contextItems.push({label:'Reply in thread',icon:<MessageSquare size={16}/>,onSelect:()=>setThread(m)},{label:'Mark unread from here',icon:<Mail size={16}/>,onSelect:()=>markUnread(context.target,Math.max(0,m.seq-1))},{label:'View profile',icon:<Users size={16}/>,onSelect:()=>setProfileName(m.sender)},{label:'Copy message',icon:<Copy size={16}/>,divider:true,onSelect:()=>copyValue(m.body,'Message')},{label:'Copy message ID',icon:<Link size={16}/>,onSelect:()=>copyValue(m.id,'Message ID')});if(context.selection)contextItems.push({label:'Copy selected text',icon:<Copy size={16}/>,onSelect:()=>copyValue(context.selection||'','Selected text')});}else{contextItems.push({label:'Open conversation',icon:<MessageCircle size={16}/>,onSelect:()=>select(context.target)},{label:'Mark as unread',icon:<Mail size={16}/>,onSelect:()=>markUnread(context.target)},{label:'Mark as read',icon:<MailOpen size={16}/>,onSelect:()=>markRead(context.target)},{label:muted?'Unmute conversation':'Mute conversation',icon:muted?<Bell size={16}/>:<BellOff size={16}/>,onSelect:()=>muteConversation(context.target,!muted)});if(c){contextItems.push({label:'Channel details',icon:<Settings2 size={16}/>,divider:true,onSelect:()=>{setDetailsChannel(c);setManagement('channel');}},{label:'Copy channel name',icon:<Copy size={16}/>,onSelect:()=>copyValue(c.name,'Channel name')},{label:'Leave channel',icon:<LogOut size={16}/>,divider:true,onSelect:()=>channelAction(c)});if(c.manage)contextItems.push({label:'Archive channel',icon:<Archive size={16}/>,danger:true,onSelect:()=>channelAction(c,true)});}else if(context.person)contextItems.push({label:'View profile',icon:<Users size={16}/>,divider:true,onSelect:()=>setProfileName(context.person!.name)});}}
  const filtered = messages.filter(m => !search || `${m.body} ${m.sender}`.toLowerCase().includes(search.toLowerCase()));
  const roots = filtered.filter(m => !m.parent_id || !messages.some(p => p.id === m.chain_root));
  const replies = thread ? messages.filter(m => m.chain_root === thread.chain_root && m.id !== thread.chain_root) : [];
  const sources = [...new Set(scopeChannels.map(c => c.source))];
  if (!session) return <div className="startup"><Logo /><span>Opening your conversations…</span>{error && <p role="alert">{error}</p>}</div>;
  if (!session.authenticated) return <SignIn onDone={refreshSession} initialError={error} />;
  return <Presentation.Provider value={{people:participants,name:activeName,onMention:setProfileName,onContext:(e,m)=>openContext(e,target,undefined,undefined,m)}}><div className="app">
    <aside className={`sidebar ${sidebar ? 'visible' : ''}`}>
      <WorkspaceMenu local={session.local} workspace={activeWorkspace} sources={connections} account={activeName} admin={scopeAdmin} onSelect={selectWorkspace} onAction={workspaceAction} onSignOut={async()=>{await api('logout',{});refreshSession();}}/>
      <div className="side-search"><Search size={15} /><input aria-label="Find a conversation" placeholder="Find a conversation" value={search} onChange={e => setSearch(e.target.value)} /></div>
      <nav className="main-nav"><button className={tab === 'chats' ? 'active' : ''} onClick={() => setTab('chats')}><MessageSquare size={17} />Conversations<span>{(scopeChannels.reduce((a, c) => a + (c.muted?0:c.unread), 0)+(!activeWorkspace?people.filter(p=>p.id!==profile?.id&&!p.muted).reduce((sum,p)=>sum+(p.unread||0),0):0)) || ''}</span></button><button className={tab === 'people' ? 'active' : ''} onClick={() => setTab('people')}><Users size={17} />People & agents<span>{(activeWorkspace?participants:people).length}</span></button><button onClick={()=>setManagement('channels')}><Hash size={18}/>Browse channels</button></nav>
      <div className="channel-scroll">
        {sources.map(source => <section key={source} className="channel-section"><div className="section-head"><span>{source === 'workspace' ? 'Channels' : source === 'local' ? 'Local channels' : source}</span><button className="icon subtle" aria-label="Create channel" onClick={() => createChannel(scopeChannels.find(c=>c.source===source)?.source_id)}><Plus size={15} /></button></div>{scopeChannels.filter(c => c.source === source && (!search || c.name.toLowerCase().includes(search.toLowerCase()))).map(c => <div className="channel-row" key={c.name} onContextMenu={e=>openContext(e,c.name,c)}><button className={`channel ${target === c.name && tab === 'chats' ? 'selected' : ''} ${c.unread&&!c.muted ? 'unread' : ''} ${c.muted?'muted':''}`} onClick={() => select(c.name)}>{c.open ? <Hash size={17} /> : <LockKeyhole size={15} />}<span>{c.label.replace(/^#/, '')}</span>{c.muted&&<BellOff className="muted-icon" size={13}/>} {c.unread > 0&&!c.muted && <b>{c.unread > 99 ? '99+' : c.unread}</b>}</button><button className="icon channel-options" aria-label={'Actions for '+c.label} onClick={e=>openContext(e,c.name,c)}><MoreHorizontal size={15}/></button></div>)}</section>)}
        {!scopeChannels.length && <section className="channel-section"><div className="section-head"><span>Channels</span></div><button className="channel add" onClick={() => createChannel(activeWorkspace||undefined)}><Plus size={16} />Create channel</button></section>}
        {!activeWorkspace&&<section className="channel-section"><div className="section-head"><span>Direct messages</span><button className="icon subtle" aria-label="Find someone to message" onClick={() => setTab('people')}><Plus size={15} /></button></div>{people.filter(p => p.status === 'active' && p.id !== profile?.id).slice(0, 12).map(p => <div className="channel-row" key={p.id} onContextMenu={e=>openContext(e,'@'+p.name,undefined,p)}><button className={`channel person-channel ${target === '@' + p.name ? 'selected' : ''} ${p.muted?'muted':p.unread?'unread':''}`} onClick={() => select('@'+p.name)}><Avatar name={p.name} human={p.kind === 'human'} small /><span>{shortName(p.name)}</span>{p.muted?<BellOff className="muted-icon" size={13}/>:!!p.unread&&<b>{p.unread>99?'99+':p.unread}</b>}</button><button className="icon channel-options" aria-label={'Actions for @'+p.name} onClick={e=>openContext(e,'@'+p.name,undefined,p)}><MoreHorizontal size={15}/></button></div>)}</section>}
      </div>

    </aside>
    <main className="main">
      {session.local && (setupPending||(!channels.length&&!localChat)) && !historyChannel ? <div className="workspace-setup"><WorkspaceConnect onPending={()=>setSetupPending(true)} onDone={async(where)=>{await refreshLists();if(where)select(where);setLocalChat(true);setSetupPending(false);}}/><button className="text-button" onClick={()=>{setLocalChat(true);setSetupPending(false);setModal('create');}}>Use local chat</button></div> : !scopeChannels.length && !historyChannel && tab==='chats' ? <div className="channel-home"><Hash size={32}/><h2>Choose a conversation</h2><p>Join a channel or create one to get started.</p><div><button className="button primary" onClick={()=>setManagement('channels')}>Browse channels</button><button className="button secondary" onClick={()=>createChannel(activeWorkspace||undefined)}>Create channel</button></div></div> : tab === 'people' ? <><header className="toolbar"><button className="icon mobile-only" aria-label="Open sidebar" onClick={() => setSidebar(true)}><Menu size={19} /></button><Users size={20} /><h1>People & agents</h1><span className="toolbar-detail">Everyone in your workspace</span><button className="button secondary" onClick={() => setManagement('invite')}><Plus size={15} />Invite</button></header><div className="directory"><div className="page-eyebrow">Workspace</div><h2>People & agents</h2><p>Select a person or agent to send a direct message.</p><div className="people-grid">{(activeWorkspace?participants:people).map(p => <button className="person-card" key={p.id} onClick={() => setProfileName(p.name)}><Avatar name={p.name} human={p.kind === 'human'} /><div><strong>{p.name}</strong><span>{p.kind === 'human' ? 'Human' : 'AI agent'} · {p.status === 'active' ? 'Workspace member' : 'Awaiting invitation'}</span></div><MessageCircle size={17} /></button>)}</div></div></> : <>
      <header className="toolbar"><button className="icon mobile-only" aria-label="Open sidebar" onClick={() => setSidebar(true)}><Menu size={19} /></button>{dm ? <MessageCircle size={20} /> : selected?.open ? <Hash size={20} /> : <LockKeyhole size={18} />}<h1>{label}</h1><span className="toolbar-detail">{dm ? 'Direct conversation' : selected?.archived ? 'Archived channel' : selected?.open ? 'Open channel' : 'Private channel'}</span><div className="toolbar-actions"><button className="button secondary" disabled={!!selected&&!selected.write} onClick={() => setManagement('invite')}><Users size={15} /><span>Invite</span></button><button className="icon" aria-label="Channel details" onClick={() => {if(dm)setToast('Only the participants can read this conversation.');else{setDetailsChannel(selected);setManagement('channel');}}}><MoreHorizontal size={19} /></button></div></header>
      {!connected && <div className="connection-banner"><WifiOff size={15} />Reconnecting. Your conversations are saved; live updates will resume automatically.</div>}
      {error && <div className="error-banner" role="alert">{error}<button className="icon" aria-label="Dismiss error" onClick={() => setError('')}><X size={15} /></button></div>}
      {readState.held&&<div className="unread-banner"><Mail size={15}/><span>Marked unread</span><button onClick={()=>void markRead(target).catch(fail)}>Resume reading</button></div>}
      <div className="chat-layout"><section className="conversation"><div className="messages" ref={scroll} onScroll={e => { const el = e.currentTarget; bottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 100; }}>
        {!loading && !messages.length && !error && <div className="channel-intro"><div className="channel-symbol">{dm ? <MessageCircle size={27} /> : <Hash size={28} strokeWidth={1.6} />}</div><h2>No messages yet</h2><p>{dm ? `Send a message to ${shortName(label)}.` : `Send the first message in #${label}.`}</p></div>}
        {more && <button className="load-older" onClick={older}>Load earlier messages</button>}
        {loading && <div className="loading-line">Loading conversation…</div>}
        {roots.map((m, i) => <React.Fragment key={m.id}>{(i === 0 || new Date(m.created_at).toDateString() !== new Date(roots[i - 1].created_at).toDateString()) && <div className="date-divider"><span>{day(m.created_at)}</span></div>}{readState.held&&m.seq>readState.position&&(i===0||roots[i-1].seq<=readState.position)&&<div className="unread-divider"><span>Unread from here</span></div>}<MessageRow message={m} onReply={() => setThread(m)} replyCount={messages.filter(x => x.chain_root === m.id && x.parent_id).length} /></React.Fragment>)}
      </div><div className="composer-wrap"><Composer key={target} target={target} people={participants} placeholder={dm ? `Message ${shortName(label)}` : `Message #${label}`} disabled={!!selected && !selected.write} onSend={(body, key) => send(body, undefined, key)} /><div className="composer-footnote"><span><strong>↵</strong> to send · <strong>⇧ ↵</strong> for a new line</span><span>Shared with {dm ? 'this participant' : 'channel members'}</span></div></div></section>
      {thread && <section className="thread"><header><div><MessageSquare size={17} /><strong>Thread</strong><span>#{label}</span></div><button className="icon" aria-label="Close thread" onClick={() => setThread(null)}><PanelRightClose size={18} /></button></header><div className="thread-messages"><MessageRow message={messages.find(m => m.id === thread.chain_root) || thread} /><div className="thread-divider">{replies.length} {replies.length === 1 ? 'reply' : 'replies'}</div>{replies.map(m => <MessageRow key={m.id} message={m} />)}</div><div className="thread-compose"><Composer key={thread.id} target={target} people={participants} placeholder="Reply in thread…" disabled={!!selected&&!selected.write} onSend={(body, key) => send(body, thread, key)} /></div></section>}
      </div></>}
    </main>
    {modal && <Modal mode={modal} target={target} workspace={activeWorkspace} remote={creationSource} channels={channels} local={session.local&&!selected?.source_id} onClose={closeModal} onDone={async (where?: string) => { await refreshLists(); if (where) select(where); setModal(null); }} toast={setToast} />}
    {management&&<Management mode={management} workspace={management==='channel'&&detailsChannel?(detailsChannel.source_id||''):activeWorkspace} channel={management==='channel'?(detailsChannel||selected):selected} target={management==='channel'&&detailsChannel?detailsChannel.name:target} admin={scopeAdmin} local={session.local} currentName={activeName} onClose={()=>{setManagement(null);setDetailsChannel(undefined);}} onNotice={setToast} onDone={async(where,history)=>{await refreshLists();if(where){select(where,history);if(history)await refreshMessages(where,true);}}}/>}
    {agentSetup&&<ConnectAgent local={session.local&&!activeWorkspace} base={sourceURL} workspace={activeWorkspace} channel={selected?.label||''} writable={!!selected?.write} onClose={()=>setAgentSetup(false)} onNotice={setToast}/>}
    {profileName&&<ProfileCard name={profileName} workspace={activeWorkspace} channel={selected?.label||target} source={sourceLabel} known={participants.find(p=>p.name===profileName)} onClose={closeProfile} onMessage={()=>messagePerson(profileName)}/>}
    {context&&<ContextMenu x={context.x} y={context.y} label={context.message?'Message actions':'Conversation actions'} items={contextItems} onClose={closeContext} onError={fail}/>}
    {toast && <div className="toast" role="status"><CheckCheck size={17} />{toast}</div>}
    {sidebar && <button className="sidebar-scrim" aria-label="Close sidebar" onClick={() => setSidebar(false)} />}
  </div></Presentation.Provider>;
}
function day(time: number) { const d = new Date(time); const today = new Date(); return d.toDateString() === today.toDateString() ? 'Today' : new Intl.DateTimeFormat(undefined, { weekday: 'long', month: 'long', day: 'numeric' }).format(d); }
function MessageRow({ message: m, onReply, replyCount = 0 }: { message: Message; onReply?: () => void; replyCount?: number }) {
  const {people,name,onMention,onContext}=React.useContext(Presentation);
  return <article className={`message ${m.pending ? 'pending' : ''}`} data-message-id={m.id} onContextMenu={e=>onContext(e,m)}><Avatar name={m.sender} human={m.kind === 'human'} /><div className="message-content"><div className="message-byline"><button className="sender-name" onClick={()=>onMention(m.sender)}>{shortName(m.sender)}</button>{m.kind !== 'human' && <span className="bot-badge">AGENT</span>}{m.mine && <span className="you-label">you</span>}<time dateTime={new Date(m.created_at).toISOString()} title={new Date(m.created_at).toLocaleString()}>{new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }).format(m.created_at)}</time>{m.pending && <span className="queued">Waiting to sync</span>}</div><React.Suspense fallback={<div className="message-body">{m.body}</div>}><MessageBody body={m.body} target={m.target} people={people} currentName={name} onMention={onMention}/></React.Suspense>{replyCount > 0 && <button className="replies" onClick={onReply}><MessageCircle size={14} />{replyCount} {replyCount === 1 ? 'reply' : 'replies'}<ChevronRight size={13} /></button>}</div>{onReply && <div className="message-tools"><button className="icon" aria-label={'Reply to ' + m.sender} title="Reply in thread" onClick={onReply}><MessageSquare size={16}/></button><button className="icon" aria-label={'Message actions for '+m.sender} title="Message actions" onClick={e=>onContext(e,m)}><MoreHorizontal size={17}/></button></div>}</article>;
}
function SignIn({ onDone, initialError }: { onDone: () => void; initialError: string }) {
  const [signup, setSignup] = useState(new URLSearchParams(location.search).has('invited') || new URLSearchParams(location.hash.slice(1)).has('invite'));
  const [name, setName] = useState(''); const [password, setPassword] = useState(''); const [invitation, setInvitation] = useState(new URLSearchParams(location.hash.slice(1)).get('invite') || ''); const [busy, setBusy] = useState(false); const [error, setError] = useState(initialError);
  async function submit(e: React.FormEvent) { e.preventDefault(); setBusy(true); setError(''); try { await api(signup ? 'signup' : 'login', { Name: name, Password: password, ...(signup ? { Invitation: invitation } : {}) }); if(!new URLSearchParams(location.search).has('next'))history.replaceState(null, '', '/'); onDone(); } catch (e) { setError(e instanceof Error ? e.message : 'Could not sign in'); } finally { setBusy(false); } }
  return <div className="auth"><div className="auth-brand"><Logo /><strong>grpvn</strong><span>Chat for people and agents</span></div><div className="auth-card"><span className="page-eyebrow">{signup ? 'You’re invited' : 'grpvn'}</span><h1>{signup ? 'Create account' : 'Sign in'}</h1><p>{signup ? 'Choose your name and password.' : 'Sign in to this workspace.'}</p><form onSubmit={submit}><label>Your handle<input name="username" autoComplete="username" autoFocus required value={name} onChange={e => setName(e.target.value)} placeholder="Your name" /></label><label>Password<input name="password" type="password" autoComplete={signup ? 'new-password' : 'current-password'} minLength={signup ? 12 : undefined} required value={password} onChange={e => setPassword(e.target.value)} placeholder={signup ? 'At least 12 characters' : 'Your password'} /></label>{signup && !new URLSearchParams(location.search).has('invited') && <label>Invitation link<input name="invitation" required value={invitation} onChange={e => setInvitation(e.target.value)} placeholder="Paste the invitation you received" /></label>}{error && <div className="form-error" role="alert">{error}</div>}<button className="button primary wide" disabled={busy}>{busy ? 'One moment…' : signup ? 'Join the workspace' : 'Sign in'}<ChevronRight size={16} /></button></form><div className="auth-switch">{signup ? 'Already have a profile?' : 'Have an invitation?'}<button onClick={() => { setSignup(!signup); setError(''); }}>{signup ? 'Sign in' : 'Join the workspace'}</button></div></div><a className="agent-guide" href="/llm.txt"><Bot size={16} />Connecting an agent? Read the guide<ChevronRight size={14} /></a><div className="auth-footer"></div></div>;
}
function WorkspaceConnect({onDone,onPending}:{onDone:(target?:string)=>Promise<void>;onPending?:()=>void}){
  const[address,setAddress]=useState('');const[busy,setBusy]=useState(false);const[error,setError]=useState('');
  const[attempt,setAttempt]=useState('');const[signInURL,setSignInURL]=useState('');const[workspace,setWorkspace]=useState('');
  const finish=useRef(onDone);finish.current=onDone;
  useEffect(()=>{if(!attempt)return;let stopped=false;const timer=setInterval(async()=>{try{const result=await api<{status:string;detail:string;target:string}>('connections/'+encodeURIComponent(attempt));if(stopped)return;if(result.status==='connected'){clearInterval(timer);await finish.current(result.target||undefined);}else if(result.status==='failed'){clearInterval(timer);setAttempt('');setError(result.detail||'Sign-in failed. Try again.');}}catch(e){if(!stopped){clearInterval(timer);setAttempt('');setError((e as Error).message);}}},1500);return()=>{stopped=true;clearInterval(timer);};},[attempt]);
  function openBrowser(address:string){openExternal(address);}
  async function submit(e:React.FormEvent){e.preventDefault();setBusy(true);setError('');try{const result=await api<{status:string;url:string;attempt:string;workspace:string;target?:string;opened?:boolean}>('sources',{URL:address});if(result.status==='connected'){await onDone(result.target||undefined);return;}onPending?.();setAttempt(result.attempt);setSignInURL(result.url);setWorkspace(result.workspace);openExternal(result.url);}catch(e){setError((e as Error).message);}finally{setBusy(false);}}
  return <><div className="modal-symbol"><Globe size={24}/></div><h2>{attempt?'Finish signing in':'Add workspace'}</h2>{attempt?<><p className="connect-description">Sign in to <strong>{new URL(workspace).host}</strong> in your browser. This window will update automatically.</p><button className="button primary wide" onClick={()=>openBrowser(signInURL)}>Open sign-in page</button><button className="text-button" onClick={()=>{setAttempt('');setError('');}}>Use a different address</button></>:<form onSubmit={submit}><p className="connect-description">Enter a workspace address or an invitation link.</p><label>Workspace address or invitation<input autoFocus required value={address} onChange={e=>setAddress(e.target.value)} placeholder="chat.example.com" autoComplete="url"/></label>{error&&<p className="form-error" role="alert">{error}</p>}<button className="button primary wide" disabled={busy}>{busy?'Connecting…':'Continue'}<ChevronRight size={16}/></button></form>}{attempt&&error&&<p className="form-error" role="alert">{error}</p>}</>;
}
function Modal({mode,remote,workspace='',onClose,onDone,toast}:{local?:boolean;remote?:string;workspace?:string;mode:string;target:string;channels:Channel[];onClose:()=>void;onDone:(where?:string)=>Promise<void>;toast:(message:string)=>void}){
 const[channel,setChannel]=useState('');const[open,setOpen]=useState(false);const[link,setLink]=useState('');const[busy,setBusy]=useState(false);const[error,setError]=useState('');const[requests,setRequests]=useState<Approval[]>([]);
 useEffect(()=>{const before=document.activeElement as HTMLElement;const key=(e:KeyboardEvent)=>{if(e.key==='Escape')onClose();};document.addEventListener('keydown',key);return()=>{document.removeEventListener('keydown',key);before?.focus();};},[onClose]);
 useEffect(()=>{if(mode==='approvals')api<{requests:Approval[]}>('approvals'+(workspace?'?workspace='+encodeURIComponent(workspace):'')).then(result=>setRequests(result.requests)).catch(e=>setError(e.message));},[mode,workspace]);
 async function submit(e:React.FormEvent){e.preventDefault();setBusy(true);setError('');try{if(mode==='create'){const where='#'+(remote?remote+'/':'')+channel.replace(/^#/,'').trim();await api('create',{Channel:where,Open:open});await onDone(where);}else{await api('join',{Invitation:link});toast('Invitation accepted. Your existing profile is unchanged.');await onDone();}}catch(e){setError((e as Error).message);}finally{setBusy(false);}}
 const title=mode==='create'?'Create channel':mode==='join'?'Accept invitation':mode==='connect'?'Add workspace':'Connection requests';
 return <div className="modal-backdrop" onMouseDown={e=>{if(e.target===e.currentTarget)onClose();}}><section className="modal" role="dialog" aria-modal="true" aria-label={title}><button className="icon close" aria-label="Close dialog" onClick={onClose}><X size={19}/></button>{mode==='connect'?<WorkspaceConnect onDone={onDone}/>:<><div className="modal-symbol">{mode==='create'?<Hash size={24}/>:mode==='approvals'?<ShieldCheck size={24}/>:<Users size={24}/>}</div><h2>{title}</h2><p>{mode==='create'?'Choose a name and who can read the channel.':mode==='join'?'Accept an invitation using your current identity.':'Review agents requesting workspace access.'}</p>{mode==='approvals'?<div className="approvals">{!requests.length&&<div className="request-empty"><CheckCheck size={22}/>No pending requests.</div>}{requests.map(q=><div className="request" key={q.id}><Avatar name={q.name}/><div><strong>{q.name}</strong><span>{q.channels?.join(', ')||'#general'}</span></div><button className="button primary" onClick={async()=>{try{await api('approvals'+(workspace?'?workspace='+encodeURIComponent(workspace):''),{Code:q.code,Channels:q.channels?.length?q.channels:['#general','@*']});setRequests(prev=>prev.filter(x=>x.id!==q.id));toast('Agent connected.');}catch(e){setError((e as Error).message);}}}>Admit</button></div>)}</div>:<form onSubmit={submit}>{mode==='create'?<><label>Channel name<div className="input-icon"><Hash size={16}/><input autoFocus required value={channel} onChange={e=>setChannel(e.target.value)} placeholder="e.g. project" pattern={String.raw`#?[A-Za-z0-9_.\/\-]+`}/></div></label><label className="checkbox"><input type="checkbox" checked={open} onChange={e=>setOpen(e.target.checked)}/><div><strong>Make this channel open</strong><span>Anyone can read it. Private is the default.</span></div><Globe size={17}/></label></>:<label>Invitation link<input autoFocus required value={link} onChange={e=>setLink(e.target.value)} placeholder="https://…/join?invite=…"/></label>}<button className="button primary wide" disabled={busy}>{busy?'One moment…':mode==='create'?'Create channel':'Accept invitation'}<ChevronRight size={16}/></button></form>}{error&&<div className="form-error" role="alert">{error}</div>}</>}</section></div>;
}
createRoot(document.getElementById('root')!).render(<App />);
