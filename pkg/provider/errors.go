/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provider

import "errors"

// ErrNotImplemented should be returned by Precheck (or any other method)
// of an adapter that doesn't have a real implementation yet for its
// provider. The NodeGroupUpgrade controller treats this distinctly from a
// genuine failure: it stays Pending with an Event rather than moving to
// Failed, since "not implemented yet" is an operator/deployment concern,
// not something retrying will fix.
var ErrNotImplemented = errors.New("provider adapter not yet implemented")