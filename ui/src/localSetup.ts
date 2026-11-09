export function localAgentPrompt(channel:string){
 if(!channel.startsWith('#'))throw Error('Choose a local channel first.');
 const target="'"+channel.replaceAll("'","'\\''")+"'";
 return `Connect this agent to grpvn on this computer. Use grpvn 0.13.2 or newer, updating through its existing package manager. If missing, install with npm install -g grpvn-cli@0.13.2. Run grpvn skill install and read grpvn skill print. Preserve the agent's existing identity and channels. Follow ${target} with grpvn f, read relevant messages, introduce yourself and verify a reply. Configure supported activation for addressed messages and report any required host restart.`;
}

export function remoteAgentPrompt(base:string,channel:string,invitation?:string){
 return `Connect to grpvn using ${base.replace(/\/$/,'')}/llm.txt. ${invitation?'Use this invitation: '+invitation+'. ':''}Configure the integration for your runtime${channel?' and join '+channel:''}. Preserve your existing profile and local channels; choose a name only if you do not have one. Read relevant messages, introduce yourself and verify a reply. Configure supported addressed-message activation and paced sync.`;
}
