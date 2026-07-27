** DO NOT EDIT content of lib folder **

# goal of project 
is creating auto MCP server (with full auto installer and certificate getting using acme.sh to serving on user provided domain and custom authorize method or base path on https ) for providing a full controller for vps server by AI Agent that give it access to using tools manage server and edit and create projects and working with git and repos and checkpoint creating  and even if user setup that fix and monitoring server and possible issues like creating services , getting log , managing tasks or processes and jobs and monitoring resource usage and finally update or upgrade system and packages 


** -------


# I created base of most tools needs to be exist . 
# it needs to we creating a task manager for running tasks in queue system.
# we also implant some chains between current exist utils like stopwatch or timer with hook to Agent can put a timer for a task and we trigger hook after time rule done.
# we also need using that sandbox vfs I putted in lib folder and creating better version of that in our utils to if Agent need isolate workspace , create and when not need that remove that.
# then we making installer and runtime detail things like setting up as service and stop item.
# then finally like below example we creating MCP server .
---------------------
```go
package main

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	// 1. Create MCP Server
	mcpSrv := server.NewMCPServer(
		"GoFrame MCP HTTP Demo",
		"1.0.0",
	)

	// 2. Add an addition tool
	tool := mcp.NewTool("add",
		mcp.WithDescription("Add two numbers"),
		mcp.WithNumber("a", mcp.Required(), mcp.Description("First number")),
		mcp.WithNumber("b", mcp.Required(), mcp.Description("Second number")),
	)

	mcpSrv.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, err := request.RequireFloat("a")
		if err != nil {
			return mcp.NewToolResultError("Parameter a is incorrect: " + err.Error()), nil
		}
		b, err := request.RequireFloat("b")
		if err != nil {
			return mcp.NewToolResultError("Parameter b is incorrect: " + err.Error()), nil
		}
		result := a + b
		return mcp.NewToolResultText(fmt.Sprintf("result: %v + %v = %v", a, b, result)), nil
	})

	// 3. StreamableHTTP
	httpSrv := server.NewStreamableHTTPServer(mcpSrv)

	// 4. Start GoFrame Server
	s := g.Server()
	s.SetPort(8080)

	// 5. Bind MCP Handlers using ghttp.WrapH
	s.BindHandler("/mcp", ghttp.WrapH(httpSrv))

	s.Run()
}
```

# we should create a batch of smart limit and truncated responde returning method to if model just need last line of result of an task can getting that 
# even if Agent noot set a limit we most check to a mistake not happens and returning unexpected long content to Agent but its not should makes a bad restriction and Agent should can ignore it and request full content if needs and truncate method should flow to many different scenario

# extra test for verify is needed 
** we assume user never even want look at source or edit and customize and just want to install it on server then set address of that to any local or cloud agent and client it has and should all things include hooks with an smart method working to users not feel any issues when using it.


# plan for once server first test release of that being ready for use  

** Agent access to terminal or shell  with handle for each platform and different possible command line they has like powershell cmd , bash sh fish zsh 

** a watch dog to accidentally model not with running to much tasks booom explode server

** some addtional tools for Agent to easy can setup useful apps like wordpress , database , docker  and docker manage , firewall , cron , inspecting network of server and toolkit for repairing and recovering and much more ...