import {test,expect} from '@playwright/test';
import {mentionNames,shortName} from '../src/api';

test('readable handles retain canonical routing and ambiguous mentions are not guessed',()=>{
 expect(shortName('reef-skua-df13@reef-skua-df13')).toBe('reef-skua-df13');
 const names=mentionNames(['reef-skua-df13@reef-skua-df13','soaring-ferret-427a@Invited_by_Tim','frane']);
 expect(names.get('soaring-ferret-427a')).toBe('soaring-ferret-427a@Invited_by_Tim');
 expect(names.get('soaring-ferret-427a@Invited_by_Tim')).toBe('soaring-ferret-427a@Invited_by_Tim');
 expect(names.get('frane')).toBe('frane');
 const collisions=mentionNames(['rook@alice','rook@bob']);
 expect(collisions.has('rook')).toBe(false);expect(collisions.get('rook@alice')).toBe('rook@alice');expect(collisions.get('rook@bob')).toBe('rook@bob');
});
