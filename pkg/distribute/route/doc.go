// Package route resolves an [ast.Directive] to a destination file path
// under the standard convention.
//
// [Decide] is a pure function: it inspects the directive's kind and
// routing key (account or commodity) and returns a [Decision] describing
// where the directive should be written and how it should be merged.
// Non-routable directive kinds yield a Decision with PassThrough set;
// the caller decides how to surface them.
package route
