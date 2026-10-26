// A policy may declare a definition of the schema's again, and cue unifies
// the two. That narrows #Principal and widens nothing: readers stays closed
// to names the schema allows.
package read

#Principal: string

project:  "app"
identity: "9da6809f038df92138c131cd16bed76d"
readers: "two words": true
