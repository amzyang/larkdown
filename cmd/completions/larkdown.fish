# Fish completions for larkdown

function __fish_larkdown_no_subcommand
    for i in (commandline -opc)
        if contains -- $i config download dl login upload ul publish diff open ocr
            return 1
        end
    end
    return 0
end

function __fish_larkdown_using_command
    # Only match when $argv[1] is the FIRST positional after `larkdown`,
    # so e.g. `larkdown download --output dl` does NOT activate `dl` context.
    set -l cmd (commandline -opc)
    if test (count $cmd) -ge 2
        if test "$cmd[2]" = "$argv[1]"
            return 0
        end
    end
    return 1
end

# Global flags (available in all contexts)
complete -c larkdown -l debug -d 'Enable HTTP request/response logging to stderr (JSONL format)'
complete -c larkdown -l help -s h -d 'show help'
complete -c larkdown -l version -s v -d 'print the version'

# Subcommands
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a config   -d 'Read config file or set field(s) if provided'
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a download -d 'Download feishu/larksuite document to markdown file'
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a dl       -d 'Download feishu/larksuite document to markdown file'
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a login    -d 'Login with Feishu OAuth to get user_access_token'
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a upload   -d 'Upload local markdown file to Feishu Wiki'
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a ul       -d 'Upload local markdown file to Feishu Wiki'
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a publish  -d 'Publish a local HTML file or directory as an online Feishu Miaoda (妙搭) app'
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a ocr      -d 'Recognize text from an image using Feishu AI OCR'
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a diff     -d 'Show diff between local markdown and remote Feishu document'
complete -c larkdown -f -n __fish_larkdown_no_subcommand -a open     -d 'Open the source Feishu document URL in the browser'

# config flags
complete -c larkdown -f -n '__fish_larkdown_using_command config' -l appId     -d 'Set app id for the OPEN API'
complete -c larkdown -f -n '__fish_larkdown_using_command config' -l appSecret -d 'Set app secret for the OPEN API'

# download / dl flags (shared between alias forms)
for _cmd in download dl
    complete -c larkdown -r -n "__fish_larkdown_using_command $_cmd" -l output    -s o -d 'Specify the output directory for the markdown files'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l recursive -s r -d 'Recursively download all child nodes of a wiki node'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l index          -d 'Generate llms.txt and docs_map.md index files'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l comments  -s c -d 'Include document comments in the exported Markdown'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l no-diff        -d 'Disable diff output when downloading'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l force     -s f -d 'Force re-download even if the remote document is unchanged'
end

# login flags
complete -c larkdown -f -n '__fish_larkdown_using_command login' -l port -d 'Local callback server port'

# upload / ul flags (shared) + .md prioritized for positional argument
for _cmd in upload ul
    complete -c larkdown -k -f -n "__fish_larkdown_using_command $_cmd" -a '(__fish_complete_suffix .md)'
    complete -c larkdown -F    -n "__fish_larkdown_using_command $_cmd"
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l source            -d 'Target Feishu document URL (mutually exclusive with --space/--parent)'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l space   -s s     -d 'Wiki space ID (optional, defaults to My Document Library)'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l parent  -s p     -d 'Parent node token (optional, for specifying location)'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l full             -d 'Full update (delete all remote blocks and re-upload) instead of the default incremental update'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l dryrun           -d 'Show what incremental update would do without making changes (incompatible with --full)'
    complete -c larkdown -f -n "__fish_larkdown_using_command $_cmd" -l verbose -s v     -d 'Show all blocks including unchanged ones (use with --dryrun)'
end

# publish flags — .html prioritized, directories and all files allowed
complete -c larkdown -k -f -n '__fish_larkdown_using_command publish' -a '(__fish_complete_suffix .html)'
complete -c larkdown -F    -n '__fish_larkdown_using_command publish'
complete -c larkdown -f -n '__fish_larkdown_using_command publish' -l name   -s n -d 'App display name (defaults to the file/dir name)'
complete -c larkdown -f -n '__fish_larkdown_using_command publish' -l app-id      -d 'Reuse an existing app to update it (app_xxx or https://miaoda.feishu.cn/app/app_xxx)'
complete -c larkdown -f -n '__fish_larkdown_using_command publish' -l new         -d 'Force creating a new app even if a publish record exists'

# diff flags — .md prioritized, all files allowed
complete -c larkdown -k -f -n '__fish_larkdown_using_command diff' -a '(__fish_complete_suffix .md)'
complete -c larkdown -F    -n '__fish_larkdown_using_command diff'
complete -c larkdown -f -n '__fish_larkdown_using_command diff' -l invert -s i -d 'Invert diff direction (remote → local)'

# open flags — .md prioritized, all files allowed
complete -c larkdown -k -f -n '__fish_larkdown_using_command open' -a '(__fish_complete_suffix .md)'
complete -c larkdown -F    -n '__fish_larkdown_using_command open'

# ocr flags — image files prioritized, all files allowed
complete -c larkdown -k -f -n '__fish_larkdown_using_command ocr' -a '(__fish_complete_suffix .png .jpg .jpeg .gif .webp)'
complete -c larkdown -F    -n '__fish_larkdown_using_command ocr'
