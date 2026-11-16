// A project's push policy, as the host declaration exports it: read by the
// pre-receive hook (../valleyhook), and by any other program that must
// read it exactly as the hook does. It is read with exactjson
// (../exactjson), which holds every key to the spelling of its field, and
// lives in this repository and is never fetched.
module the-valley/pushpolicy

go 1.24

require the-valley/exactjson v0.0.0

replace the-valley/exactjson => ../exactjson
