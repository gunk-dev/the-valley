// Read access: the two consents a principal needs to read a project, read
// out of git commits with cue, and the evaluator that intersects them. The
// web publisher reads through it. Go, standard library only.
module the-valley/readaccess

go 1.24

require the-valley/exactjson v0.0.0

// The exported JSON is read with exactjson (../exactjson), which holds
// every key to the spelling of its field. It lives in this repository and
// is never fetched.
replace the-valley/exactjson => ../exactjson
