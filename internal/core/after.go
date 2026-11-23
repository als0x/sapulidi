package core

// AfterRemover is optionally implemented by a Category. The engine calls AfterRemove once per candidate of that
// category whose removal returned no error, and never in a dry run. Proof and Scan stay free of side effects;
// this is where a category records that something was really done.
type AfterRemover interface {
	AfterRemove(env *Env, c Candidate)
}
