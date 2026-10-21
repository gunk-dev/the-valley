// The composed verification policy: the floor, the project-type template
// it names, and the project's own layer, composed by cue under the
// packaged schema. The integrator (../integrator) reads its checks and its
// digest through this module, the restacker (../restack) reads its cap on
// automatic versions, and the deriver (../derive) reads the whole policy,
// so all three read one composition. Standard library only.
module the-valley/composed

go 1.24
