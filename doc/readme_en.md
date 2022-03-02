
# pong-go


- [中文](https://github.com/pingworlds/pong/README.md)
- [English](https://github.com/pingworlds/pong/doc/readme_en.md)

pong-go is a golang language implementation of the pong proxy protocol.


## Associated Projects

- pong-protocol 
  
  <https://github.com/pingworlds/pong-protocol>

- pong-go install shell 
  
  <https://github.com/pingworlds/pong-install>
  
- pong-go install example 
  
  <https://github.com/pingworlds/pong-config-example>


- ping 
  
  pong protocol android app <https://github.com/pingworlds/ping>


## install


Download the latest compiled version <https://github.com/pingworlds/pong/releases>



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

For more details on the pong protocol, see <https://github.com/pingworlds/pong-protocol>




## transport protocols

pong-go supports the following network standard protocols as transport protocols.
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

pong-go also supports the following proxy protocols
- pong
- shadowsokcs 
- vless
- socks5
- qsocks (a lite version of socks5 without the handshake process)

Note: All proxy protocols, only plaintext is supported


## startup

pong-go startup parameters

    -c string
        config file, remote mode defalut config file is "remote.json" ,local mode default config file is "local.json"
    -d string
        work dir (default ". /")
    -m string
        run mode: local or remote (default "remote")



## local mode

running in a local network environment, or as a platform proxy client.

### Start local

    $ pong -m local -d $workdir

    will look for the file "local.json" in the $workdir directory


    or

    $ pong -m local -d $workdir -c $config.json



### local configuration

The main elements include a socks5/qsocks listening service, a set of remote nodes, some optional runtime parameters, and proxy rules

    {
        "listeners": [
            {
                "transport": "tcp",
                "host": ":11984",
                "protocol": "socks"
            }
        ],
        "points": [
            {
                "transport": "h2",
                "host": "$domain",
                "protocol": "pong",       
                "path": "/h2c-pong",           
                "clients": [
                   "0f608556-88f7-11ec-a8a3-0242ac120002"
                ],
                "insecureSkip": true,
                "disabled": false
            }      
        ] 
    }

 



## remote mode

The remote is deployed on a remote proxy server, and the following is a pong node configuration hidden behind the web server

    { 
        "listeners": [
            {
                "transport": "h2c",
                "host": "127.0.0.1:21984",
                "protocol": "pong",          
                "clients": [
                    "0f608556-88f7-11ec-a8a3-0242ac120002"
                ]          
            }       
        ]
    }



### Start remote

    pong -d $workdir

    will look for the file "remote.json" in the $workdir directory


    or

    pong -m remote -d $workdir -c $config.json



## Configuration files

The core content of the configuration file is the node, a node is called a point or a peer, and includes the following fields

### Required fields

#### protocol string  

pong,vless,socks,ss,qsocks


#### transport string   

h3,h2,h2c,http,https,ws,wss,tcp,tls


#### host string

Network address, domain name or IP address, IP address needs to include the port number


### Optional fields

#### path string
    
http entry path, try to use a deep long path with random characters to avoid malicious probes


#### clients []string

A set of client Id, must be a legitimate 16-byte uuid, pong/vless is used to identify the legitimacy of the client
    
#### ssl  
	certFile string    
	keyFile string
	sni string 
	insecureSkip bool //whether to skip certificate validation


#### disabled bool

Temporarily disable the node



## web server


Set with caddy,nginx and other web servers, please refer to <https://github.com/pingworlds/pong-congfig-example>


## local advanced

Compared with remote mode, local mode supports more parameters

    "autoTry": true,
    "rejectMode": true,
    "domainMode": "proxy",
    "ipMode": "proxy",  
    "perMaxCount": 100,  


### autoTry bool

Automatically try remote proxies for domains or IPs that fail to connect directly.

When this option is turned on, theoretically no other rules are needed, but it may be better to turn it on at the same time.


### rejectMode bool

Block ads, etc. based on a block list.


### domainMode string

Optional values

- "proxy" //all proxies
- "direct" //all direct connections
- "white" //white list direct, rest proxy, black list exception
- "black" //blacklist proxy, rest direct, whitelist exceptions


Exceptions indicate that a domain or ip exists in both the white and black lists



### ipMode string

Same as domainMode



### perMaxCount

The maximum number of concurrent proxy requests supported by a network connection, default value 100, beyond this value, a new connection will be opened and the idle network connection will be automatically disconnected


### rule

Set rules by domain and ip, rules file must be located in $workDir directory.

The domain rule is located in $workDir/domain/ 

ip rule is located in $workDir/ip/ 


rule General configuration fields

- type
  
  list type, "white", "black", "reject" means white list, black list, block list respectively

- name
  
  Configuration item name

- fileName
  
  File name

- url
  
  source

- disable
 
  Disable false or true


Example

    {
        "name": "project-list",
        "fileName": "project-list.txt",      
        "type": "project"
    }

 

#### domain rule

The domain rule file has one rule per line, in three formats:

- Domain name
    
        google.com // match *.google.com.*

- domain name
  
        full:www.apple.com //exact match www.apple.com

- Regular expressions
  
        regexp:^ewcdn[0-9]{2}\.nowe\.com$


Add a set of domain rules to the configuration file

    "domainRules": [
        {
            "name": "reject-list",
            "fileName": "reject-list.txt",
            "url": "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/reject-list.txt",
            "type": "reject"
        },
        {
            "name": "direct-list",
            "fileName": "direct-list.txt",
            "url": "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/direct-list.txt",
            "type": "white"
        },
        {
            "name": "google-cn",
            "fileName": "google-cn.txt",
            "url": "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/google-cn.txt",
            "type": "white"
        },
        {
            "name": "apple-cn",
            "fileName": "apple-cn.txt",
            "url": "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/apple-cn.txt",
            "type": "white"
        },
        {
            "name": "proxy-list",
            "fileName": "proxy-list.txt",
            "url": "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/proxy-list.txt",
            "type": "black"
        },
        {
            "name": "greatfire",
            "fileName": "greatfire.txt",
            "url": "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/greatfire.txt",
            "type": "black"
        },
        {
            "name": "gfw",
            "fileName": "gfw.txt",
            "url": "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/gfw.txt",
            "type": "black"
        }
    ]



#### ip rule

Format: 1.0.32.0/19

Add a set of ip rules to the configuration file

    "ipRules": [
        {
            "name": "china_ipv4_ipv6_list",
            "fileName": "china_ipv4_ipv6_list.txt",
            "url": "https://raw.githubusercontent.com/LisonFan/china_ip_list/master/china_ipv4_ipv6_list",
            "type": "white"
        }
    ]



## aar

pong-go can be compiled into an aar file using the gomobile bind command to be used as a third-party library for the platform client.



### doh service

Note: doh service is limited by the network environment, the speed is not ideal, so use it carefully.

When used as a platform client library, you may need the dns function. pong-go has a built-in doh function to simplify the development of the platform client doh function.

A list of available doh services



    "workDohs": [
        {
            "name": "Cloudflare",
            "path": "https://1dot1dot1dot1.cloudflare-dns.com"
        },
        {
            "name": "Cloudflare(1.1.1.1)",
            "path": "https://1.1.1.1/dns-query"
        },
        {
            "name": "Cloudflare(1.0.0.1)",
            "path": "https://1.0.0.1/dns-query"
        },
        {
            "name": "Google",
            "path": "https://dns.google/dns-query"
        },
        {
            "name": "Google(8.8.8.8)",
            "path": "https://8.8.8.8/dns-query"
        },
        {
            "name": "Google(8.8.4.4)",
            "path": "https://8.8.4.4/dns-query"
        },
        {
            "name": "DNS.SB",
            "path": "https://doh.dns.sb/dns-query"
        },
        {
            "name": "OpenDNS",
            "path": "https://doh.opendns.com/dns-query"
        },
        {
            "name": "Quad9",
            "path": "https://dns.quad9.net/dns-query"
        },
        {
            "name": "twnic",
            "path": "https://dns.twnic.tw/dns-query"
        },
        {
            "name": "AliDNS",
            "path": "https://dns.alidns.com/dns-query"
        },
        {
            "name": "DNSPOD",
            "path": "https://doh.pub/dns-query"
        },
        {
            "name": "360",
            "path": "https://doh.360.cn/dns-query"
        }
    ]