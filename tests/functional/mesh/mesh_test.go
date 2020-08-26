package mesh

import (
	"bytes"
	"context"
	_ "github.com/fortytw2/leaktest"
	"github.com/project-receptor/receptor/tests/functional/lib/receptorcontrol"
	"github.com/project-receptor/receptor/tests/functional/lib/utils"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Test that a mesh starts and that connections are what we expect and that
// each node's view of the mesh converges
func TestMeshStartup(t *testing.T) {
	testTable := []struct {
		filename string
	}{
		{"mesh-definitions/flat-mesh-tcp.yaml"},
		{"mesh-definitions/random-mesh-tcp.yaml"},
		{"mesh-definitions/tree-mesh-tcp.yaml"},
		{"mesh-definitions/flat-mesh-udp.yaml"},
		{"mesh-definitions/random-mesh-udp.yaml"},
		{"mesh-definitions/tree-mesh-udp.yaml"},
		{"mesh-definitions/flat-mesh-ws.yaml"},
		{"mesh-definitions/random-mesh-ws.yaml"},
		{"mesh-definitions/tree-mesh-ws.yaml"},
	}
	t.Parallel()
	for _, data := range testTable {
		filename := data.filename
		t.Run(filename, func(t *testing.T) {
			t.Parallel()
			t.Logf("starting mesh")
			mesh, err := NewCLIMeshFromFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			defer mesh.WaitForShutdown()
			defer mesh.Destroy()
			t.Logf("waiting for mesh")
			ctx, _ := context.WithTimeout(context.Background(), 20*time.Second)
			err = mesh.WaitForReady(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Test that each Node can ping each Node
			for _, nodeSender := range mesh.Nodes() {
				controller := receptorcontrol.New()
				t.Logf("connecting to %s", nodeSender.ControlSocket())
				err = controller.Connect(nodeSender.ControlSocket())
				if err != nil {
					t.Fatal(err)
				}
				for nodeIDResponder := range mesh.Nodes() {
					t.Logf("pinging %s", nodeIDResponder)
					response, err := controller.Ping(nodeIDResponder)
					if err != nil {
						t.Error(err)
					}
					t.Logf("%v", response)
				}
				controller.Close()
			}
		})
	}
}

// Test that a mesh starts and that connections are what we expect
func TestMeshConnections(t *testing.T) {
	testTable := []struct {
		filename string
	}{
		{"mesh-definitions/flat-mesh-tcp.yaml"},
		{"mesh-definitions/random-mesh-tcp.yaml"},
		{"mesh-definitions/tree-mesh-tcp.yaml"},
		{"mesh-definitions/flat-mesh-udp.yaml"},
		{"mesh-definitions/random-mesh-udp.yaml"},
		{"mesh-definitions/tree-mesh-udp.yaml"},
		{"mesh-definitions/flat-mesh-ws.yaml"},
		{"mesh-definitions/random-mesh-ws.yaml"},
		{"mesh-definitions/tree-mesh-ws.yaml"},
	}
	t.Parallel()
	for _, data := range testTable {
		filename := data.filename
		t.Run(filename, func(t *testing.T) {
			t.Parallel()
			mesh, err := NewCLIMeshFromFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			defer mesh.WaitForShutdown()
			defer mesh.Destroy()
			yamlDat, err := ioutil.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}

			data := YamlData{}

			err = yaml.Unmarshal(yamlDat, &data)
			if err != nil {
				t.Fatal(err)
			}
			ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
			for connectionsReady := mesh.CheckConnections(); !connectionsReady; connectionsReady = mesh.CheckConnections() {
				time.Sleep(100 * time.Millisecond)
				if ctx.Err() != nil {
					t.Error("Timed out while waiting for connections:")
				}
			}
		})
	}
}

// Test that a mesh starts and that connections are what we expect
func TestMeshShutdown(t *testing.T) {
	//defer leaktest.Check(t)()
	testTable := []struct {
		filename string
	}{
		{"mesh-definitions/random-mesh-tcp.yaml"},
		{"mesh-definitions/random-mesh-udp.yaml"},
		{"mesh-definitions/random-mesh-ws.yaml"},
	}
	for _, data := range testTable {
		filename := data.filename
		t.Run(filename, func(t *testing.T) {
			mesh, err := NewLibMeshFromFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			ctx, _ := context.WithTimeout(context.Background(), 20*time.Second)
			err = mesh.WaitForReady(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mesh.Destroy()
			mesh.WaitForShutdown()

			// Check that the connections are closed
			pid := os.Getpid()
			pidString := "pid=" + strconv.Itoa(pid)
			done := false
			var out bytes.Buffer
			for timeout := 10 * time.Second; timeout > 0 && !done; {
				out = bytes.Buffer{}
				cmd := exec.Command("ss", "-tuanp")
				cmd.Stdout = &out
				err := cmd.Run()
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), pidString) {
					done = true
					break
				}
				time.Sleep(100 * time.Millisecond)
				timeout -= 100 * time.Millisecond
			}
			if done == false {
				t.Errorf("Timed out while waiting for backends to close:\n%s", out.String())
			}
		})
	}
}

func TestTCPSSLConnections(t *testing.T) {
	t.Parallel()
	testTable := []struct {
		listener string
	}{
		{"tcp-listener"},
		{"ws-listener"},
	}
	for _, data := range testTable {
		listener := data.listener
		t.Run(listener, func(t *testing.T) {
			t.Parallel()

			// Setup the mesh directory
			baseDir := filepath.Join(os.TempDir(), "receptor-testing")
			// Ignore the error, if the dir already exists thats fine
			os.Mkdir(baseDir, 0755)
			tempdir, err := ioutil.TempDir(baseDir, "certs-*")
			os.Mkdir(tempdir, 0755)
			caKey, caCrt, err := utils.GenerateCert(tempdir, "ca")
			if err != nil {
				t.Fatal(err)
			}
			key1, crt1, err := utils.GenerateCert(tempdir, "node1")
			if err != nil {
				t.Fatal(err)
			}
			key2, crt2, err := utils.GenerateCertWithCA(tempdir, "node2", caKey, caCrt)
			if err != nil {
				t.Fatal(err)
			}

			// Setup our mesh yaml data
			data := YamlData{}
			data.Nodes = make(map[string]*YamlNode)

			// Generate a mesh where each node n is connected to only n+1 and n-1
			// if they exist
			data.Nodes["node1"] = &YamlNode{
				Connections: map[string]YamlConnection{},
				Nodedef: []interface{}{
					map[interface{}]interface{}{
						"tls-server": map[interface{}]interface{}{
							"name":              "cert1",
							"key":               key1,
							"cert":              crt1,
							"requireclientcert": true,
							"clientcas":         caCrt,
						},
					},
					map[interface{}]interface{}{
						listener: map[interface{}]interface{}{
							"tls": "cert1",
						},
					},
				},
			}
			data.Nodes["node2"] = &YamlNode{
				Connections: map[string]YamlConnection{
					"node1": YamlConnection{
						Index: 1,
						TLS:   "client-cert2",
					},
				},
				Nodedef: []interface{}{
					map[interface{}]interface{}{
						"tls-server": map[interface{}]interface{}{
							"name": "server-cert2",
							"key":  key2,
							"cert": crt2,
						},
					},
					map[interface{}]interface{}{
						"tls-client": map[interface{}]interface{}{
							"name":               "client-cert2",
							"key":                key2,
							"cert":               crt2,
							"insecureskipverify": true,
						},
					},
					map[interface{}]interface{}{
						listener: map[interface{}]interface{}{
							"tls": "server-cert2",
						},
					},
				},
			}
			data.Nodes["node3"] = &YamlNode{
				Connections: map[string]YamlConnection{
					"node2": YamlConnection{
						Index: 2,
						TLS:   "client-insecure",
					},
				},
				Nodedef: []interface{}{
					map[interface{}]interface{}{
						"tls-client": map[interface{}]interface{}{
							"name":               "client-insecure",
							"key":                "",
							"cert":               "",
							"insecureskipverify": true,
						},
					},
					map[interface{}]interface{}{
						listener: map[interface{}]interface{}{},
					},
				},
			}
			mesh, err := NewCLIMeshFromYaml(data)
			if err != nil {
				t.Fatal(err)
			}
			defer mesh.WaitForShutdown()
			defer mesh.Destroy()

			ctx, _ := context.WithTimeout(context.Background(), 20*time.Second)
			err = mesh.WaitForReady(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Test that each Node can ping each Node
			for _, nodeSender := range mesh.Nodes() {
				controller := receptorcontrol.New()
				err = controller.Connect(nodeSender.ControlSocket())
				if err != nil {
					t.Fatal(err)
				}
				for nodeIDResponder := range mesh.Nodes() {
					response, err := controller.Ping(nodeIDResponder)
					if err != nil {
						t.Error(err)
					}
					t.Logf("%v", response)
				}
				controller.Close()
			}
		})
	}
}

func TestTCPSSLClientAuthFailNoKey(t *testing.T) {
	t.Parallel()
	testTable := []struct {
		listener string
	}{
		{"tcp-listener"},
		{"ws-listener"},
	}
	for _, data := range testTable {
		listener := data.listener
		t.Run(listener, func(t *testing.T) {
			t.Parallel()

			// Setup the mesh directory
			baseDir := filepath.Join(os.TempDir(), "receptor-testing")
			// Ignore the error, if the dir already exists thats fine
			os.Mkdir(baseDir, 0755)
			tempdir, err := ioutil.TempDir(baseDir, "certs-*")
			os.Mkdir(tempdir, 0755)
			_, caCrt, err := utils.GenerateCert(tempdir, "ca")
			if err != nil {
				t.Fatal(err)
			}
			key1, crt1, err := utils.GenerateCert(tempdir, "node1")
			if err != nil {
				t.Fatal(err)
			}

			// Setup our mesh yaml data
			data := YamlData{}
			data.Nodes = make(map[string]*YamlNode)

			// Generate a mesh where each node n is connected to only n+1 and n-1
			// if they exist
			data.Nodes["node1"] = &YamlNode{
				Connections: map[string]YamlConnection{},
				Nodedef: []interface{}{
					map[interface{}]interface{}{
						"tls-server": map[interface{}]interface{}{
							"name":              "cert1",
							"key":               key1,
							"cert":              crt1,
							"requireclientcert": true,
							"clientcas":         caCrt,
						},
					},
					map[interface{}]interface{}{
						listener: map[interface{}]interface{}{
							"tls": "cert1",
						},
					},
				},
			}
			data.Nodes["node2"] = &YamlNode{
				Connections: map[string]YamlConnection{
					"node1": YamlConnection{
						Index: 1,
						TLS:   "client-insecure",
					},
				},
				Nodedef: []interface{}{
					map[interface{}]interface{}{
						"tls-client": map[interface{}]interface{}{
							"name":               "client-insecure",
							"key":                "",
							"cert":               "",
							"insecureskipverify": true,
						},
					},
					map[interface{}]interface{}{
						listener: map[interface{}]interface{}{},
					},
				},
			}
			mesh, err := NewCLIMeshFromYaml(data)
			if err != nil {
				t.Fatal(err)
			}
			defer mesh.WaitForShutdown()
			defer mesh.Destroy()

			ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
			err = mesh.WaitForReady(ctx)
			if err == nil {
				t.Fatal("Receptor client auth was expected to fail but it succeeded")
			}
		})
	}
}

func TestTCPSSLClientAuthFailBadKey(t *testing.T) {
	t.Parallel()
	testTable := []struct {
		listener string
	}{
		{"tcp-listener"},
		{"ws-listener"},
	}
	for _, data := range testTable {
		listener := data.listener
		t.Run(listener, func(t *testing.T) {
			t.Parallel()

			// Setup the mesh directory
			baseDir := filepath.Join(os.TempDir(), "receptor-testing")
			// Ignore the error, if the dir already exists thats fine
			os.Mkdir(baseDir, 0755)
			tempdir, err := ioutil.TempDir(baseDir, "certs-*")
			os.Mkdir(tempdir, 0755)
			_, caCrt, err := utils.GenerateCert(tempdir, "ca")
			if err != nil {
				t.Fatal(err)
			}
			key1, crt1, err := utils.GenerateCert(tempdir, "node1")
			if err != nil {
				t.Fatal(err)
			}

			key2, crt2, err := utils.GenerateCert(tempdir, "node2")
			if err != nil {
				t.Fatal(err)
			}

			// Setup our mesh yaml data
			data := YamlData{}
			data.Nodes = make(map[string]*YamlNode)

			// Generate a mesh where each node n is connected to only n+1 and n-1
			// if they exist
			data.Nodes["node1"] = &YamlNode{
				Connections: map[string]YamlConnection{},
				Nodedef: []interface{}{
					map[interface{}]interface{}{
						"tls-server": map[interface{}]interface{}{
							"name":              "cert1",
							"key":               key1,
							"cert":              crt1,
							"requireclientcert": true,
							"clientcas":         caCrt,
						},
					},
					map[interface{}]interface{}{
						listener: map[interface{}]interface{}{
							"tls": "cert1",
						},
					},
				},
			}
			data.Nodes["node2"] = &YamlNode{
				Connections: map[string]YamlConnection{
					"node1": YamlConnection{
						Index: 1,
						TLS:   "client-insecure",
					},
				},
				Nodedef: []interface{}{
					map[interface{}]interface{}{
						"tls-client": map[interface{}]interface{}{
							"name":               "client-insecure",
							"key":                key2,
							"cert":               crt2,
							"insecureskipverify": true,
						},
					},
					map[interface{}]interface{}{
						listener: map[interface{}]interface{}{},
					},
				},
			}
			mesh, err := NewCLIMeshFromYaml(data)
			if err != nil {
				t.Fatal(err)
			}
			defer mesh.WaitForShutdown()
			defer mesh.Destroy()

			ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
			err = mesh.WaitForReady(ctx)
			if err == nil {
				t.Fatal("Receptor client auth was expected to fail but it succeeded")
			}
		})
	}
}

func TestCosts(t *testing.T) {
	t.Parallel()
	// Setup our mesh yaml data
	data := YamlData{}
	data.Nodes = make(map[string]*YamlNode)

	// Generate a mesh where each node n is connected to only n+1 and n-1
	// if they exist
	data.Nodes["node1"] = &YamlNode{
		Connections: map[string]YamlConnection{},
		Nodedef: []interface{}{
			map[interface{}]interface{}{
				"tcp-listener": map[interface{}]interface{}{
					"cost": 4.5,
					"nodecost": map[interface{}]interface{}{
						"node2": 2.6,
						"node3": 3.2,
					},
				},
			},
		},
	}
	data.Nodes["node2"] = &YamlNode{
		Connections: map[string]YamlConnection{
			"node1": YamlConnection{
				Index: 0,
			},
		},
		Nodedef: []interface{}{},
	}
	data.Nodes["node3"] = &YamlNode{
		Connections: map[string]YamlConnection{
			"node1": YamlConnection{
				Index: 0,
			},
		},
		Nodedef: []interface{}{},
	}
	data.Nodes["node4"] = &YamlNode{
		Connections: map[string]YamlConnection{
			"node1": YamlConnection{
				Index: 0,
			},
		},
		Nodedef: []interface{}{},
	}
	mesh, err := NewCLIMeshFromYaml(data)
	if err != nil {
		t.Fatal(err)
	}
	defer mesh.WaitForShutdown()
	defer mesh.Destroy()

	ctx, _ := context.WithTimeout(context.Background(), 20*time.Second)
	err = mesh.WaitForReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Test that each Node can ping each Node
	for _, nodeSender := range mesh.Nodes() {
		controller := receptorcontrol.New()
		err = controller.Connect(nodeSender.ControlSocket())
		if err != nil {
			t.Fatal(err)
		}
		for nodeIDResponder := range mesh.Nodes() {
			response, err := controller.Ping(nodeIDResponder)
			if err != nil {
				t.Error(err)
			}
			t.Logf("%v", response)
		}
		controller.Close()
	}

}

func TestWorkCancel(t *testing.T) {
	t.Parallel()
	// Setup our mesh yaml data
	data := YamlData{}
	data.Nodes = make(map[string]*YamlNode)
	workCommand := map[interface{}]interface{}{
		"service": "echosleep",
		"command": "bash",
		"params":  "-c \"for i in {1..5}; do echo $i; sleep 2;done\"",
	}
	// Generate a mesh with 2 nodes
	data.Nodes["node1"] = &YamlNode{
		Connections: map[string]YamlConnection{},
		Nodedef: []interface{}{
			map[interface{}]interface{}{
				"tcp-listener": map[interface{}]interface{}{
					"cost": 4.5,
					"nodecost": map[interface{}]interface{}{
						"node2": 2.6,
					},
				},
			},
		},
	}
	data.Nodes["node2"] = &YamlNode{
		Connections: map[string]YamlConnection{
			"node1": YamlConnection{
				Index: 0,
			},
		},
		Nodedef: []interface{}{
			map[interface{}]interface{}{
				"work-command": workCommand,
			},
		},
	}

	mesh, err := NewCLIMeshFromYaml(data)
	if err != nil {
		t.Fatal(err)
	}
	defer mesh.WaitForShutdown()
	defer mesh.Destroy()

	ctx, _ := context.WithTimeout(context.Background(), 20*time.Second)
	err = mesh.WaitForReady(ctx)
	if err != nil {
		t.Fatal(err)
	}

	nodes := mesh.Nodes()

	controller := receptorcontrol.New()
	err = controller.Connect(nodes["node1"].ControlSocket())
	if err != nil {
		t.Fatal(err)
	}
	workID, err := controller.WorkSubmit("node2", "echosleep")
	if err != nil {
		t.Fatal(err)
	}
	// controller closes after a work submit, so must reopen
	controller = receptorcontrol.New()
	err = controller.Connect(nodes["node1"].ControlSocket())
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ = context.WithTimeout(context.Background(), 20*time.Second)
	err = controller.AssertWorkRunning(ctx, workID)
	if err != nil {
		t.Fatal(err)
	}
	controller.WorkCancel(workID)
	ctx, _ = context.WithTimeout(context.Background(), 20*time.Second)
	err = controller.AssertWorkCancelled(ctx, workID)
	if err != nil {
		t.Fatal(err)
	}
	controller.WorkRelease(workID)
	err = controller.AssertWorkReleased(workID)
	if err != nil {
		t.Fatal(err)
	}
	controller.Close()

}

func benchmarkLinearMeshStartup(totalNodes int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		// Setup our mesh yaml data
		b.StopTimer()
		data := YamlData{}
		data.Nodes = make(map[string]*YamlNode)

		// Generate a mesh where each node n is connected to only n+1 and n-1
		// if they exist
		for i := 0; i < totalNodes; i++ {
			connections := make(map[string]YamlConnection)
			nodeID := "Node" + strconv.Itoa(i)
			if i > 0 {
				prevNodeID := "Node" + strconv.Itoa(i-1)
				connections[prevNodeID] = YamlConnection{
					Index: 0,
				}
			}
			data.Nodes[nodeID] = &YamlNode{
				Connections: connections,
				Nodedef: []interface{}{
					map[interface{}]interface{}{
						"tcp-listener": map[interface{}]interface{}{},
					},
				},
			}
		}
		b.StartTimer()

		// Reset the Timer because building the yaml data for the mesh may have
		// taken a bit
		mesh, err := NewCLIMeshFromYaml(data)
		if err != nil {
			b.Fatal(err)
		}
		ctx, _ := context.WithTimeout(context.Background(), 20*time.Second)
		err = mesh.WaitForReady(ctx)
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		mesh.Destroy()
		mesh.WaitForShutdown()
		b.StartTimer()
	}
}

func BenchmarkLinearMeshStartup100(b *testing.B) {
	benchmarkLinearMeshStartup(100, b)
}

func BenchmarkLinearMeshStartup10(b *testing.B) {
	benchmarkLinearMeshStartup(10, b)
}
