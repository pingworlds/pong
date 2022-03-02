
# pong-go

- [中文](README.md)
- [English](doc/readme_en.md)



pong-go 是 pong 代理协议的 golang 语言实现。


## 关联项目

- pong-protocol 
  
  <https://github.com/pingworlds/pong-protocol>

- pong-go install shell 
  
  <https://github.com/pingworlds/pong-install>
  
- pong-go install example 
  
  <https://github.com/pingworlds/pong-config-example>


- ping 
  
  pong protocol android app <https://github.com/pingworlds/ping>


## 安装


下载最新编译版 <https://github.com/pingworlds/pong/releases>



## pong proxy protocol

pong 是一种结合了socks5与http2特性的代理协议，支持在一条网络连接上多路复用同时并发数十数百个代理请求。

pong协议的优势：

- 避免“连接阻塞”
 
因为安全和隐私保护需要，代理服务通常放置在web server之后，

    src <---> proxy-local <---> web server <---> proxy server <---> dst 

web server对单一客户端有着最大并发连接数限制，一般不会超过10，打开一个新闻/图片网站首页往往需要并发50-100个代理请求，连接数耗尽瞬间耗尽，后续代理请求排队等待连接释放，于是发生客户端卡顿假死，通常只能重启客户端来强制释放连接。

在90%以上的时间里，一个pong客户端只需要维持一个网络连接，可以有效避免“连接阻塞”导致的卡顿。
  
- 0 open 时间 
  
因为是在一条已经打开的网络连接上并发代理，无需耗费时间去为每个代理请求打开网络连接和握手验证，收到代理请求会立即转发，速度上pong会优于其它协议。

关于pong协议的细节，请参阅 <https://github.com/pingworlds/pong-protocol>




## transport protocols

pong-go 支持以下网络标准协议作为传输协议：
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

pong-go 同时支持以下代理协议
- pong
- shadowsokcs 
- vless
- socks5
- qsocks (没有握手过程的精简版socks5)

注意：所有代理协议，仅支持明文


## 启动

pong-go 启动参数

    -c string
        config file, remote mode defalut config file is "remote.json" ,local mode default config file is "local.json"
    -d string
        work dir (default "./")
    -m string
        run mode: local or remote (default "remote")



## local 模式

部署运行在本地网络环境，或者作为平台代理客户端的pong协议实现库。

### 启动 local

    $ pong  -m local -d  $workdir

    会在 $workdir 目录下寻找文件 "local.json"


    or

    $ pong  -m local -d  $workdir  -c  $config.json



### local 配置

主要内容包括一个socks5/qsocks监听服务，一组远程节点，一些可选的运行参数以及代理规则

    {
        "listens": [
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

 



## remote 模式

remote 部署在远程代理服务器，下面是一个隐藏在web server之后的pong节点配置

    { 
        "listens": [
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



### 启动 remote

    pong  -d  $workdir

    会在 $workdir 目录下寻找文件 "remote.json"


    or

    pong  -m remote -d $workdir  -c  $config.json



## 配置文件

配置文件的核心内容是节点，一个节点称为一个point或者一个peer,包括以下字段

### 必须字段

#### protocol  string  

pong,vless,socks,ss,qsocks


#### transport string   

h3,h2,h2c,http,https,ws,wss,tcp,tls


#### host      string

网络地址，域名或IP地址，IP地址需要包括端口号


### 可选字段

#### path         string
    
http入口路径，尽量使用随机字符的深层长路径，以避免被恶意探测


#### clients      []string

一组客户端Id，必须是合法的16字节的uuid，pong/vless用于鉴别客户端的合法性
    
#### ssl  
	certFile     string    
	keyFile      string
	sni          string 
	insecureSkip bool   //是否跳过证书验证


#### disabled     bool

临时禁用节点



## web server


与 caddy,nginx 等 web server 配合设置，请参考 <https://github.com/pingworlds/pong-congfig-example>


## local 高级

相对于remote模式，local模式下支持更多参数配置

    "autoTry": true,
    "rejectMode": true,
    "domainMode": "proxy",
    "ipMode": "proxy",  
    "perMaxCount": 100,  


### autoTry  bool

直连失败的域名或IP,自动尝试远程代理。

该选项开启后，理论上不再需要其它规则,同时开启或许效果更佳。


### rejectMode  bool

根据拦截名单拦截广告等


### domainMode  string

可选值

-  "proxy"  //全部代理
-  "direct" //全部直连
-  "white"  //白名单直连，其余代理，黑名单例外
-  "black"  //黑名单代理，其余直连，白名单例外


例外表示一个域名或ip在白、黑名单中同时存在的情形



### ipMode string

同 domainMode



### perMaxCount

一条网络连接支持的最大并发代理请求数量, 默认值 100，超过此值，会新打开一条连接，空闲的网络连接会自动断开


### rule

按域名和ip分别设置规则，规则文件必须位于 $workDir 目录。

domain rule  位于  $workDir/domain/ 

ip rule 位于 $workDir/ip/ 


rule 通用配置字段

- type
  
  名单类型，"white","black","reject" 分别表示白名单，黑名单，拦截名单

- name
  
  配置项名称

- fileName
  
  文件名

- url
  
  来源

- disable
 
  禁用  false or true


样例

    {
        "name": "reject-list",
        "fileName": "reject-list.txt",      
        "type": "reject"
    }

 

####  domain rule

domain rule 文件每行一条规则,三种格式:

- 范域名
    
        google.com          //匹配   *.google.com.*

- 域名
  
        full:www.apple.com  //精确匹配  www.apple.com

- 正则表达式
  
        regexp:^ewcdn[0-9]{2}\.nowe\.com$




在配置文件中添加一组域名规则

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

  


#### ip 规则

格式：1.0.32.0/19

在配置文件中添加一组ip规则

    "ipRules": [
        {
            "name": "china_ipv4_ipv6_list",
            "fileName": "china_ipv4_ipv6_list.txt",
            "url": "https://raw.githubusercontent.com/LisonFan/china_ip_list/master/china_ipv4_ipv6_list",
            "type": "white"
        }
    ]



## aar

pong-go 可以使用 gomobile bind 命令编译成 aar 文件作为平台客户端的第三方库使用。

ping <https://github.com/pingworlds/ping>   


### doh 服务

注： doh服务受限于网络环境，速度并不理想，慎用。

作为平台客户端库使用时，可能会需要 dns 功能，pong-go内置了doh 功能，简化平台客户端doh功能的开发。

一组可用的doh 服务列表

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