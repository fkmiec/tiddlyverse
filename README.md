# Tiddlyverse
A multi-wiki tiddlyweb server for TiddlyWiki written in Go.

NOTE - This was forked from https://git.sr.ht/~hokiegeek/tiddlybucket. The foundational work to function as a TiddlyWeb server is mostly from there. This fork adds multi-wiki support, including creating, listing, deleting wikis and the necessary support for custom paths required to make serving multiple wikis from a single server possible without the aid of a proxy server.

The name Tiddlyverse is a play on universe or multiverse, emphasizing the capability of a single server to support multiple TiddlyWikis. In addition to supporting the TiddlyWeb protocol, it includes the following additions:

- Support for serving many wikis from the same server. 
  - Each wiki is given a unique path off of the server root. For example, the wiki "~MyWiki" will be at `http://<host>:<port>/MyWiki`. 
- A landing page at the server root listing all current wikis
  - A link to each wiki
  - A link to rename a wiki. The rename action prompts you for a new name. The new name will affect the path in the URL and the folder name on the server. It has no impact on the title used inside the wiki.
  - A link to delete a wiki. The delete action is confirmed and the wiki is moved to the "trash" folder on the server. At present, there is no automatic deletion by the server, so you would have to delete it manually. 
- A page for creating a new wiki from a template
  - A template is a TiddlyWiki HTML file with any plugins or content tiddlers that you want to be part of the foundation for a wiki. All subsequent tiddlers will be saved individually in the wiki folder on the server.

## Getting Started

### Building

Nothing fancy. Just `go -build cmd/tiddlyverse/main.go -o tiddlyverse`

NOTE - To be cleaned up, but at the moment, there is a Linux binary included in the git repo

### Running

Execute `./tiddlyverse --host <hostname or ip address> <wiki_location>`

- The <wiki_location> should be specified using the file prefix (e.g. `file:///home/user1/tiddlyverse/dist`)
- You may also optionally specify 
- `--port <port>` 
- Various readers, writers and credentials parameters supported by TiddlyBucket (NOTE - These parameters and features have not been tested on this fork of the codebase)
- Minimum requirement is to specify a host and a wiki_location as shown above
  - The `host` parameter is required to enable multiple wikis from the same server using a separate path for each wiki. A tiddler called **$:/config/tiddlyweb/host** with the host value is added to each wiki's tiddlers folder to let TiddlyWiki know that relative path URLs are relative to the full path specified and not just the host:port.
  - The `wiki_location` is the top-level storage folder. It will contain three folders: 
    - **wikis** - where wikis are stored
    - **templates** - where template wikis are stored
    - **trash** - where deleted wikis are temporarily stored in case you need to recover them

The "dist" folder in the repo includes a templates directory with a couple of sample templates. You can copy that dist folder to wherever you want to locate your wikis and specify that dist folder as the wiki_location on the command line. 

### What you'll see

The directory structure under the dist folder in the repo represents the required directory structure for Tiddlyverse. 

![Directory Structure](/assets/images/directories.png)

After executing the command to start the server, navigate to the server root (e.g. `http://127.0.0.1:8080`) and you'll be presented with a welcome page and an empty list of wikis. From there, you can create your first wiki by clicking on the link at the bottom of the page. 

![Home Start](/assets/images/home_start.png)

The Add Wiki page lists your available template files and prompts you to provide a name for your wiki. The name will be part of the URL path after it is created (e.g. `http://127.0.0.1:8080/MyNewWiki`). 

![Create Wiki](/assets/images/create_wiki.png)

After clicking the template link to create the new wiki, you'll be redirected to the newly created wiki. 

![New Wiki](/assets/images/new_wiki.png)

You may wish to add a tiddler called **$:/SiteDescription** with a short description for your new wiki. It will be used for the description in the list of wikis on the welcome page. 

![Home End](/assets/images/home_end.png)

In the listing of wikis, you'll notice a Rename and a Delete link. You may rename a wiki, which will change the path in the URL and the folder name on the server. You may also delete a wiki by clicking that link. It will ask you to confirm and upon confirmation, it will move the wiki to the trash directory. This gives you the chance to change your mind or salvage some tiddlers you may have forgotten you needed. You can permanently delete it later manually on the server.  

## Templates

To create a new wiki, you must have at least one template. I've included, as examples, a basic server edition TiddlyWiki and one loaded with many plugins I find useful. It is perfectly possible to use only the basic server edition template and add plugins to the resulting wiki using the drag and drop approach, but by creating a template, you can create a new empty wiki, easily at any time, that already packs all your favorite plugin goodness. 

## Creating Templates

A template is a TiddlyWiki .html file configured the way you want it, foundationally, for creating new wikis. The best way to create a new template is to follow the TiddlyWiki NodeJS instructions for building a single file wiki based on one of the editions that ship with the NodeJS implementation (You can look that up on [tiddlywiki.com(http://tiddlywiki.com)]). I suggest you start with the server edition and add plugins by listing them in the plugin.info file before executing: 

`tiddlywiki mywiki --output <your target dir> --build index`

It is possible to open the resulting index.html file or grab some other existing wiki and run it locally, add plugins to it by drag and drop method (again, look this up on [tiddlywiki.com(http://tiddlywiki.com)]). However, if you take this approach, be aware that the TiddlyWeb plugin is required to operate with a server, rather than as a standalone single file wiki plus one of the many other "saver" methods, and it will not be present in any TiddlyWiki being run as a single HTML file (the server sync mechanism is incompatible with running as a standalone wiki). You'll need to remember to add the TiddlyWeb plugin as the last step to finalize your template wiki. 

Once ready, your template file should be added to the templates folder along with a small .txt file containing a sentence or two describing your template, which will be displayed when you are selecting a template from which to build a new wiki. 

## Included Templates

### tw_5_2_5_loaded
- **Codemirror editor plugin** with TiddlyWiki5 syntax highlighting to make editing wiki syntax easier
- **Stroll plugins** for a number of nice features for "slipbox" or "second brain" type usage. 
  - Backlinks - Links displayed at the bottom of a tiddler to any other tiddler that links to it. 
  - Re-linking - Automatically fixes links when you rename a tiddler. 
  - Automatic Lists - Navigate bulleted and numbered lists with tab and shift-tab to increase or decrease depth
  - Autocomplete Links - Provides context help when typing links
  - Second Story River - A second column to display more tiddlers, for instance, to view one while editing another. 
- **KeyNav plugin** - Provides keyboard navigation of the story river. Key combinations can be edited in the settings menu.
- **Browser-Storage plugin** - Saves tiddlers locally in browser storage. This is not a good solution for storage in general, but it is a good solution for temporarily storing tiddlers when you lose connection with the server and either close your browser or navigate away from the wiki by accident. Re-syncing browser-storage tiddlers to the server is not part of this plugin's functionality, so see below. 
- **TiddlyWebAdapter-Browser-Storage-Sync plugin** - Enables syncing of locally stored tiddlers to the server when server connection is restored. TiddlyWiki will resync on its own as long as you don't close the browser or navigate away. This fills that gap. 
- **Alert-Suppression plugin** - Suppresses repeating alert banners when you go offline temporarily and want to keep working on the wiki. Knowing that you have local browser storage and will eventually reconnect and resync with the server means you can be more confident about continuing your work and will want the banner to go away. The synchronization cloud icon in the sidebar will continue to show red, reminding you that you are not in sync with the server ... until you are. 

### base_server

This is the standard TiddlyWiki server edition with the core of TiddlyWiki and the TiddlyWeb plugin required for using the server. 

# Known Issues

This is very much an initial effort and some bugs are likely. At present, the following issues are known. 

- Any shadow tiddler included in the template wiki file will take precedence over any subsequent change to that same tiddler. It has something to do with the order in which shadow tiddlers are loaded. This was discovered when, for instance, a chosen color palette was saved to the server, but upon reloading the wiki, the original palette value was back. The workaround is to find the offending shadow tiddler in the template file and delete it. 

 
