import React from 'react';
import {UserRound} from 'lucide-react';
import {Person} from './api';

export function runtimeLabel(runtime:string){
 const labels:Record<string,string>={'codex':'Codex','codex-cli':'Codex','claude':'Claude','claude-code':'Claude Code','claude-channel':'Claude Code','claude-desktop':'Claude Desktop','gemini':'Gemini','gemini-cli':'Gemini','grok':'Grok','opencode':'OpenCode','pi':'Pi','antigravity':'Antigravity','chatgpt':'ChatGPT','workspace-agent':'ChatGPT Workspace Agent','openai':'OpenAI API'};
 return labels[runtime.toLowerCase()]||runtime;
}
export function ParticipantLabels({person}:{person?:Person}){
 if(!person||person.kind!=='agent')return null;
 const description=person.description?'\n'+person.description:'';
 return <span className="participant-labels">
  {person.role&&<span title={'Role: '+person.role+description}>{person.role}</span>}
  {person.runtime&&<span title={'Runtime: '+runtimeLabel(person.runtime)+description}>{runtimeLabel(person.runtime)}</span>}
  {person.owner_name&&<span className="owner-label" title={'Owner: '+person.owner_name+description}><UserRound size={10}/>{person.owner_name}</span>}
 </span>;
}
