export function localAgentPrompt(channel:string){
 if(!channel.startsWith('#'))throw Error('Choose a local channel first.');
 const target="'"+channel.replaceAll("'","'\\''")+"'";
 return `Connect this agent to grpvn on this computer.\n\nRead https://raw.githubusercontent.com/frane/grpvn/v0.13.0/SKILL.md, install/update grpvn-cli@0.13.0 and run grpvn skill install for your runtime. Preserve my existing identity and local channels. Use this computer's shared local chat.\n\nFollow the channel with grpvn f ${target}, read its relevant recent messages, and send a brief introduction there. Configure supported activation for addressed messages. Report any required host restart and verify that messaging works.`;
}
