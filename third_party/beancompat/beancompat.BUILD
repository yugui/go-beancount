filegroup(
    name = "parse_fixtures",
    srcs = glob(["fixtures/parse/*.json"]),
    visibility = ["//visibility:public"],
)

filegroup(
    name = "check_fixtures",
    srcs = glob(["fixtures/check/*.json"]),
    visibility = ["//visibility:public"],
)

exports_files(["fixtures/README.md"])

filegroup(
    name = "implementations_py",
    srcs = glob(
        ["implementations/**/*.py"],
        exclude = ["implementations/**/__pycache__/**", "implementations/**/*.pyc"],
    ),
    visibility = ["//visibility:public"],
)

filegroup(
    name = "strategies_py",
    srcs = glob(
        ["strategies/**/*.py"],
        exclude = ["strategies/**/__pycache__/**", "strategies/**/*.pyc"],
    ),
    visibility = ["//visibility:public"],
)

filegroup(
    name = "tests_py",
    srcs = glob(
        ["tests/**/*.py"],
        exclude = ["tests/**/__pycache__/**", "tests/**/*.pyc"],
    ),
    visibility = ["//visibility:public"],
)
