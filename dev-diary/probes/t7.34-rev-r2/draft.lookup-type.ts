// The cut proposal lookup must say a missing id reads undefined, as
// the old Map lookup did.
import type { DraftController } from './draft';

type Lookup = DraftController['cutProposals'][string];
export const missingReadsUndefined: undefined extends Lookup ? true : false = true;
