import React, {useRef,useState} from 'react';
import {createPortal} from 'react-dom';
import Markdown,{defaultUrlTransform} from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import {visit} from 'unist-util-visit';
import {Check,Copy,Download,FileText,X} from 'lucide-react';
import {fileURL,Person} from './api';
const highlightOptions={detect:false,aliases:{typescript:['tsx'],javascript:['jsx']}};

function mentions(options:{names:string[]}){
 const names=new Set(options.names);
 return (tree:any)=>{visit(tree,'text',(node:any,index:number|undefined,parent:any)=>{
  if(index===undefined||!parent||parent.type==='link')return;
  const parts:any[]=[];let last=0;const re=/(^|[\s([{])@([\p{L}\p{N}_.~\-]+)/gu;let match;
  while((match=re.exec(node.value))){let name=match[2];while(name.endsWith('.')&&!names.has(name))name=name.slice(0,-1);if(!names.has(name))continue;const start=match.index+match[1].length;if(start>last)parts.push({type:'text',value:node.value.slice(last,start)});parts.push({type:'link',url:'grpvn-mention:'+encodeURIComponent(name),children:[{type:'text',value:'@'+name}]});last=start+name.length+1;}
  if(!parts.length)return;if(last<node.value.length)parts.push({type:'text',value:node.value.slice(last)});parent.children.splice(index,1,...parts);return index+parts.length;
 });};
}
function CodeBlock({children}:{children?:React.ReactNode}){
 const pre=useRef<HTMLPreElement>(null);const[copied,setCopied]=useState(false);
 const child=React.Children.toArray(children)[0] as React.ReactElement<{className?:string}>;
 const language=child?.props?.className?.match(/language-([\w+-]+)/)?.[1]||'code';
 return <div className="code-block"><div className="code-toolbar"><span>{language}</span><button aria-label="Copy code" onClick={async()=>{await navigator.clipboard.writeText(pre.current?.textContent||'');setCopied(true);setTimeout(()=>setCopied(false),1800);}}>{copied?<Check size={14}/>:<Copy size={14}/>} {copied?'Copied':'Copy'}</button></div><pre ref={pre}>{children}</pre></div>;
}
function Image({src,alt}:{src:string;alt:string}){
 const[open,setOpen]=useState(false);const[failed,setFailed]=useState(false);
 return <><button className="image-attachment" onClick={()=>setOpen(true)} aria-label={'Preview '+alt}>{failed?<span><FileText size={20}/>{alt} · Open image</span>:<img loading="lazy" src={src} alt={alt} onError={()=>setFailed(true)}/>}</button>{open&&createPortal(<div className="preview-backdrop" role="dialog" aria-modal="true" aria-label="Image preview" onClick={()=>setOpen(false)}><div className="preview-header"><strong>{alt}</strong><a href={src+'&download=1'} aria-label="Download image" onClick={e=>e.stopPropagation()}><Download size={20}/></a><button aria-label="Close preview" onClick={()=>setOpen(false)}><X size={22}/></button></div><img className="image-full" src={src} alt={alt} onClick={e=>e.stopPropagation()}/></div>,document.body)}</>;
}
function FileLink({href,name}:{href:string;name:string}){
 const[open,setOpen]=useState(false);const[text,setText]=useState('');const[error,setError]=useState('');const[loading,setLoading]=useState(false);
 const extension=name.split('.').pop()?.toLowerCase()||'';
 const language=({js:'javascript',jsx:'jsx',ts:'typescript',tsx:'tsx',py:'python',rs:'rust',sh:'bash',yml:'yaml',md:'markdown',txt:'text'} as Record<string,string>)[extension]||extension;
 const previewable=/^(txt|md|json|js|jsx|ts|tsx|go|rs|py|sh|yaml|yml|toml|css|html|xml|sql|c|cpp|h|java|rb|swift|log|csv)$/.test(extension);
 async function preview(){setOpen(true);if(text||loading)return;setLoading(true);try{const response=await fetch(href);if(!response.ok)throw Error('The file is unavailable or access was removed.');if(Number(response.headers.get('content-length'))>256000)throw Error('Download this file to view it; preview supports up to 256 KB.');const content=await response.text();if(content.length>256000)throw Error('File is too large to preview.');setText(content);}catch(e){setError((e as Error).message);}finally{setLoading(false);}}
 const fence='`'.repeat((text.match(/`+/g)||[]).reduce((length,run)=>Math.max(length,run.length+1),3));
 return <><span className="file-attachment"><FileText size={26}/><span><strong>{name}</strong><span>{extension.toUpperCase()||'File'}{previewable?' · Preview available':''}</span></span>{previewable&&<button className="button secondary" onClick={preview}>Preview</button>}<a className="icon" href={href+'&download=1'} aria-label={'Download '+name}><Download size={18}/></a></span>{open&&createPortal(<div className="modal-backdrop" onClick={()=>setOpen(false)}><section className="file-preview modal" role="dialog" aria-label={'Preview '+name} onClick={e=>e.stopPropagation()}><header><strong>{name}</strong><a className="icon" href={href+'&download=1'} aria-label="Download file"><Download size={18}/></a><button className="icon" aria-label="Close preview" onClick={()=>setOpen(false)}><X size={20}/></button></header>{loading?<p>Loading preview…</p>:error?<p role="alert">{error}</p>:<Markdown rehypePlugins={[[rehypeHighlight,highlightOptions]]} components={{pre:CodeBlock}}>{fence+language+'\n'+text+'\n'+fence}</Markdown>}</section></div>,document.body)}</>;
}
const MemberContext=React.createContext<{currentName:string;onMention?:(name:string)=>void}>({currentName:''});
function MessageLink({href,children}:{href?:string;children?:React.ReactNode}){
 const {currentName,onMention}=React.useContext(MemberContext);
 return href?.startsWith('grpvn-mention:')?<button type="button" className={'mention '+(decodeURIComponent(href.slice(14))===currentName?'mention-me':'')} title={'View @'+decodeURIComponent(href.slice(14))} onClick={()=>onMention?.(decodeURIComponent(href.slice(14)))}>{children}</button>:href?.startsWith('/v1/files/')?<FileLink href={href} name={String(children)}/>:<a href={href} target="_blank" rel="noreferrer noopener">{children}</a>;
}
function MessageImage({src,alt}:{src?:string|Blob;alt?:string}){
 return typeof src==='string'&&src.startsWith('/v1/files/')?<Image src={src} alt={alt||'Image'}/>:<span className="external-image">{alt||'External image'}</span>;
}
const markdownComponents={pre:CodeBlock,a:MessageLink,img:MessageImage};
export function MessageBody({body,target,people=[],currentName='',onMention}:{body:string;target:string;people?:Person[];currentName?:string;onMention?:(name:string)=>void}){
 const names=[...people.map(p=>p.name),currentName].filter(Boolean);
 return <MemberContext.Provider value={{currentName,onMention}}><div className="message-body markdown"><Markdown skipHtml remarkPlugins={[remarkGfm,[mentions,{names}]]} rehypePlugins={[[rehypeHighlight,highlightOptions]]}
  urlTransform={url=>{if(/^grpvn-file:[0-9A-HJKMNP-TV-Z]{26}$/.test(url))return fileURL(url.slice(11),target);if(url.startsWith('grpvn-mention:')){try{const name=decodeURIComponent(url.slice(14));return names.includes(name)?'grpvn-mention:'+encodeURIComponent(name):'';}catch{return '';}}return defaultUrlTransform(url);}}
  components={markdownComponents}>{body}</Markdown></div></MemberContext.Provider>;
}
