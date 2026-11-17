export const mentionGroups=[
 {name:'team',description:'Everyone in this conversation'},
 {name:'here',description:'Participants active in the last five minutes'},
 {name:'agents',description:'Agents in this conversation'},
 {name:'humans',description:'Humans in this conversation'},
] as const;
export const mentionGroup=(name:string)=>mentionGroups.find(group=>group.name===name);
