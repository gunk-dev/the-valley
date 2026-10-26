// The policy/read a new project starts from (schema/read.cue). It names the
// operator, who reads every project as an ordinary named reader, and no
// one else. Replace the project's name, and replace the identity with 32
// random lowercase hex digits, such as `openssl rand -hex 16` prints, once,
// when the policy is first written. Add anyone to make the project public
// where the instance allows it.
package read

project:  "site"
identity: "273d5668702918ea7ae079de374db96b"
readers: operator: true
