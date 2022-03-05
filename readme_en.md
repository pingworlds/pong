
# pong-go

- [Chinese](README.md)
- [English](readme_en.md)



pong-go is an open source implementation of the pong proxy protocol in the golang language.



## Install

- linux shell
  
    curl -o- -L https://github.com/pingworlds/pong-install//releases/latest/download/install.sh | bash

  If the shell does not execute correctly, please try the following.


- Distribution

  Download the latest compiled version of <https://github.com/pingworlds/pong/releases>

- Source code

  Download the source code directly

- Configuration commands
  
  Please refer to the guide <https://github.com/pingworlds/pong-install>


- aar

For use as a third-party library for android clients, use the gomobile bind command to compile to aar, refer to gomobile.txt(gomobile.txt)




## transport protocols

pong-go supports the following standard protocols as transport protocols.
- tcp
- tls
- http
- https
- http2
- h2c
- http3
- ws
- wss


## proxy protocols

pong-go also supports the following proxy protocols.
- pong
- shadowsokcs 
- vless
- socks5
- qsocks (a lite version of socks5 without the handshake process)

Note: All proxy protocols, only plaintext is supported


## Associated Projects

- pong-protocol 
  
  <https://github.com/pingworlds/pong-protocol>

- pong-go install  
  
  <https://github.com/pingworlds/pong-install>
  
- pong-go install example 
  
  <https://github.com/pingworlds/pong-config-example>


- ping 
  
  pong protocol android app <https://github.com/pingworlds/ping>



## pong proxy protocol

pong is a proxy protocol that combines the features of socks5 and http2 to support multiplexing tens of hundreds of proxy requests on a single network connection at the same time.

Advantages of the pong protocol.

- Avoid "connection blocking"
 
For security and privacy reasons, proxy services are usually placed after the web server

    src <---> proxy-local <---> web server <---> proxy server <---> dst 

web server has a maximum concurrent connection limit for a single client, generally not more than 10, open a news / picture site home page often requires concurrent 50-100 proxy requests, the number of connections exhausted instantly exhausted, the subsequent proxy requests queued for connection release, so the client stalled false death, usually only restart the client to force the release of the connection.

In more than 90% of the time, a pong client only needs to maintain a network connection, which can effectively avoid the "connection blocking" caused by the lag.
  
- 0 open time 
  
Because it is a concurrent proxy on an open network connection, there is no need to spend time to open the network connection and handshake authentication for each proxy request, and proxy requests are forwarded immediately upon receipt, making pong faster than other protocols.

For more information about the pong protocol, see <https://github.com/pingworlds/pong-protocol>.


 