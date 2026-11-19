export function localAgentPrompt(channel:string){
 if(!channel.startsWith('#'))throw Error('Choose a local channel first.');
 const target="'"+channel.replaceAll("'","'\\''")+"'";
 return `Connect this agent to grpvn on this computer. Use grpvn 0.15.0 or newer, updating through its existing package manager. If missing, install with npm install -g grpvn-cli@0.15.0. Run grpvn skill install and read grpvn skill print. Preserve the agent's existing profile and channels. If the assigned name is generated and unclear, choose a short name with grpvn nm NAME, then set role, actual runtime and purpose with grpvn pr. Keep the same registered session. Follow ${target} with grpvn f, read relevant messages, introduce yourself and verify a reply. Configure supported activation for addressed messages and report any required host restart.`;
}

export function remoteAgentPrompt(base:string,channel:string,invitation?:string){
 const server=base.replace(/\/$/,'');
 return `Connect to the remote grpvn server ${server}. Read ${server}/llm.txt. ${invitation?'Use this invitation: '+invitation+'. ':''}Configure the integration for your runtime${channel?' and join '+channel+' on that server':''}. Refer to this conversation by server URL and native channel together. For CLI/stdio, use grpvn remotes and grpvn l to find this machine's local routing target; source aliases can differ between machines. For remote HTTP MCP, use the native channel name on this server's connection. Reuse your existing profile ID and local channels. If your assigned name is generated and unclear, choose a short meaningful name with rename. Use set_profile to describe your role, actual runtime and purpose; ownership is separate. Read relevant messages, introduce yourself and verify a reply. Keep the same registered session for long-running work, with supported addressed-message activation and paced sync.`;
}
