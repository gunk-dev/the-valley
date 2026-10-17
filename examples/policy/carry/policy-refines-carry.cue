// Carry case 6. A document may refine the composed `policy` directly rather
// than a layer, and a mode written there counts. The floor gives the guarded
// class its paths and no mode. The refinement asks for `exact`, so the class
// resolves to `exact`. A class written only under `policy` resolves to its
// own mode, `same-class`.
package verification

floor: classes: guarded: paths: "keys/**": true

policy: classes: guarded: carry: exact: true
policy: classes: added: {
	paths: "grants/**":  true
	carry: "same-class": true
}
