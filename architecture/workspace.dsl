// C4 model of a customer website built on baseplate (levels 1 and 2).
// Render with Structurizr, see ARCHITECTURE.md.
workspace "baseplate" "A customer website built on baseplate." {

    !identifiers hierarchical

    model {
        developer = person "Developer" "Maintains a customer project: main.go, theme, plugins."
        editor = person "Editor" "Maintains content and media in the admin."
        visitor = person "Visitor" "Reads the website and submits forms."

        site = softwareSystem "Customer website" "Customer project built on the baseplate Go module." {
            app = container "baseplate binary" "Admin, static site generator and public endpoints (forms) in one process. Customer main.go + baseplate + plugins." "Go"
            db = container "Database" "Entries, components, relations, users, sessions, plugin tables. Replaceable via Config.Store." "SQLite (modernc.org/sqlite)" "Database"
            media = container "Media store" "Uploaded files in DataDir/media." "File system" "Folder"
            dist = container "Static site" "HTML, Markdown variants, sitemap.xml, robots.txt, llms.txt, assets and media in dist/. Served by the binary or any static host." "Static files" "Folder"
        }

        smtp = softwareSystem "SMTP server" "Delivers form notifications. Without one, mails are only logged." "External"
        crawlers = softwareSystem "Search engines & LLM crawlers" "Index the static site." "External"

        developer -> site.app "Builds and operates" "go build, CLI (serve, build, user add)"
        editor -> site.app "Edits content, uploads media, triggers builds" "HTTPS, /admin"
        visitor -> site.dist "Reads pages" "HTTPS"
        visitor -> site.app "Submits forms" "HTTPS, POST /api/forms/{id}"
        crawlers -> site.dist "Reads pages, sitemap.xml, robots.txt, llms.txt" "HTTPS"

        site.app -> site.db "Reads and writes" "SQL"
        site.app -> site.media "Stores and serves uploads" "File I/O"
        site.app -> site.dist "Writes the static build" "File I/O"
        site.app -> smtp "Sends form notifications" "SMTP"
    }

    views {
        systemContext site "Context" "Level 1: system context." {
            include *
            autoLayout lr
        }

        container site "Containers" "Level 2: containers." {
            include *
            autoLayout lr
        }

        styles {
            element "Element" {
                color #ffffff
            }
            element "Person" {
                shape Person
                background #08427b
            }
            element "Software System" {
                background #1168bd
            }
            element "Container" {
                background #438dd5
            }
            element "Database" {
                shape Cylinder
            }
            element "Folder" {
                shape Folder
            }
            element "External" {
                background #999999
            }
        }
    }
}
