"""Platform constraints for goplug plugin fixtures.

Go's -buildmode=plugin and the stdlib plugin package that goplug
wraps are supported only on linux, macos, and freebsd. Fixture
go_binary targets built with linkmode="plugin" fail to build on
other hosts; the goplug_test target that depends on those fixtures
via `data` inherits the failure. Applying this constraint via
`target_compatible_with` makes `bazel build //...` and `bazel test
//...` skip the incompatible targets on unsupported hosts rather
than error out, so the same command line works everywhere.
"""

PLUGIN_COMPATIBLE_PLATFORMS = select({
    "@platforms//os:linux": [],
    "@platforms//os:macos": [],
    "@platforms//os:freebsd": [],
    "//conditions:default": ["@platforms//:incompatible"],
})
